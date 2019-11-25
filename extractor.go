package imports

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"

	"github.com/src-d/enry/v2"
)

type Config struct {
	// Out is a destination to write JSON output to during Extract.
	Out io.Writer
	// Num is the maximal number of goroutines when extracting imports.
	// Zero value means use NumCPU.
	Num int
	// MaxSize is the maximal size of files in bytes that will be parsed.
	// For files larger than this, only a sample of this size will be used for language detection.
	// Library may use the sample to try extracting imports, or may return an empty list of imports.
	MaxSize int64
}

// NewExtractor creates an extractor with a given configuration. See Config for more details.
func NewExtractor(c Config) *Extractor {
	if c.Num <= 0 {
		c.Num = runtime.NumCPU()
	}
	if c.Out == nil {
		c.Out = os.Stdout
	}
	if c.MaxSize == 0 {
		c.MaxSize = 1 * 1024 * 1024
	}
	return &Extractor{
		enc:     json.NewEncoder(c.Out),
		num:     c.Num,
		maxSize: c.MaxSize,
	}
}

type File struct {
	Path    string   `json:"file"`
	Lang    string   `json:"lang,omitempty"`
	Imports []string `json:"imports,omitempty"`
}

type Extractor struct {
	mu  sync.Mutex
	enc *json.Encoder

	num     int
	maxSize int64
}

type extractJob struct {
	fname string
	path  string
	buf   []byte // sample buffer
}

// Extract imports recursively from a given directory. The root is a root of the project's repository and rel is the
// relative path inside it that will be processed. Two paths exists to allow the library to potentially parse dependency
// manifest files that are usually located in the root of the project.
func (e *Extractor) Extract(root, rel string) error {
	var (
		jobs      chan *extractJob
		errc      chan error
		wg        sync.WaitGroup
		sampleBuf []byte
	)
	if e.num != 1 {
		jobs = make(chan *extractJob, e.num)
		errc = make(chan error, e.num)
		for i := 0; i < e.num; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				e.worker(jobs, errc)
			}()
		}
		// no sample buffer, it's per-routine
	} else {
		sampleBuf = make([]byte, e.maxSize)
	}
	// TODO(dennwc): expand relative imports and use dependency manifests in the future
	err := filepath.Walk(filepath.Join(root, rel), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		} else if info.IsDir() {
			return nil // continue
		}
		fname, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		args := extractJob{fname: fname, path: path, buf: sampleBuf}
		if e.num == 1 {
			return e.processFile(args)
		}
		select {
		case jobs <- &args:
		case err = <-errc:
			return err
		}
		return nil
	})
	if e.num == 1 {
		return err
	}
	close(jobs)
	if err != nil {
		return err
	}
	wg.Wait()
	select {
	case err = <-errc:
		return err
	default:
	}
	return nil
}

// ExtractFrom extracts imports from a given file content, assuming it had a given path.
//
// The path is used for language detection only. It won't access any files locally and won't fetch dependency manifests
// as Extract may do.
func (e *Extractor) ExtractFrom(path string, content []byte) (*File, error) {
	lang := enry.GetLanguage(path, content)
	f := &File{Path: path, Lang: lang}
	if lang == enry.OtherLanguage {
		// unknown language - skip
		return f, nil
	}
	l := LanguageByName(lang)
	if l == nil {
		// emit the file-language mapping; no imports
		return f, nil
	}
	// import extraction
	list, err := l.Imports(content)
	if err != nil {
		return f, err
	}
	sort.Strings(list)
	f.Imports = list
	return f, nil
}

func (e *Extractor) worker(jobs <-chan *extractJob, errc chan<- error) {
	buf := make([]byte, e.maxSize)
	for args := range jobs {
		args.buf = buf
		if err := e.processFile(*args); err != nil {
			errc <- err
			return
		}
	}
}

func (e *Extractor) processFile(args extractJob) error {
	if len(args.buf) == 0 {
		panic("buffer must be set")
	}
	f, err := os.Open(args.path)
	if err != nil {
		return err
	}
	defer f.Close()

	data := args.buf
	total := 0
	for left := data; len(left) > 0; {
		n, err := f.Read(left)
		total += n
		left = left[n:]
		if err == io.EOF {
			break
		} else if err != nil {
			return err
		}
	}
	_ = f.Close()
	data = data[:total]
	return e.processAndEmit(args.fname, args.path, data)
}

func (e *Extractor) processAndEmit(fname, path string, data []byte) error {
	f, err := e.ExtractFrom(path, data)
	if err != nil {
		return err
	}
	f.Path = fname

	e.mu.Lock()
	defer e.mu.Unlock()
	return e.enc.Encode(f)
}
