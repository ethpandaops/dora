package templates

import (
	"bufio"
	"embed"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/sirupsen/logrus"
	"github.com/tdewolff/minify"

	"github.com/pk910/light-beaconchain-explorer/utils"
)

var logger = logrus.New().WithField("module", "templates")

var (
	//go:embed *
	Files embed.FS
)

var templateCache = make(map[string]*template.Template)
var templateCacheMux = &sync.RWMutex{}
var templateFuncs = utils.GetTemplateFuncs()

// compile time check for templates
//var _ error = CompileTimeCheck(fs.FS(Files))

func GetTemplate(files ...string) *template.Template {
	name := strings.Join(files, "-")

	if utils.Config.Frontend.Debug {
		templateFiles := make([]string, len(files))
		copy(templateFiles, files)
		for i := range files {
			if strings.HasPrefix(files[i], "templates") {
				templateFiles[i] = files[i]
			} else {
				templateFiles[i] = "templates/" + files[i]
			}
		}
		return template.Must(template.New(name).Funcs(template.FuncMap(templateFuncs)).ParseFiles(templateFiles...))
	}

	templateCacheMux.RLock()
	if templateCache[name] != nil {
		defer templateCacheMux.RUnlock()
		return templateCache[name]
	}
	templateCacheMux.RUnlock()

	tmpl := template.New(name).Funcs(template.FuncMap(templateFuncs))
	tmpl = template.Must(parseTemplateFiles(tmpl, readFileFS(Files), files...))
	templateCacheMux.Lock()
	defer templateCacheMux.Unlock()
	templateCache[name] = tmpl
	return templateCache[name]
}

func readFileFS(fsys fs.FS) func(string) (string, []byte, error) {
	return func(file string) (name string, b []byte, err error) {
		name = path.Base(file)
		b, err = fs.ReadFile(fsys, file)

		if utils.Config.Frontend.Minify {
			// minfiy template
			m := minify.New()
			m.AddFunc("text/html", minifyTemplate)
			b, err = m.Bytes("text/html", b)
			if err != nil {
				panic(err)
			}
		}
		return
	}
}

func minifyTemplate(m *minify.M, w io.Writer, r io.Reader, _ map[string]string) error {
	// remove newlines and spaces
	m1 := regexp.MustCompile(`([ \t]+)?[\r\n]+`)
	m2 := regexp.MustCompile(`([ \t])[ \t]+`)
	rb := bufio.NewReader(r)
	for {
		line, err := rb.ReadString('\n')
		if err != nil && err != io.EOF {
			return err
		}
		line = m1.ReplaceAllString(line, "")
		line = m2.ReplaceAllString(line, " ")
		if _, errws := io.WriteString(w, line); errws != nil {
			return errws
		}
		if err == io.EOF {
			break
		}
	}
	return nil
}

func parseTemplateFiles(t *template.Template, readFile func(string) (string, []byte, error), filenames ...string) (*template.Template, error) {
	for _, filename := range filenames {
		name, b, err := readFile(filename)
		if err != nil {
			return nil, err
		}
		s := string(b)
		var tmpl *template.Template
		if t == nil {
			t = template.New(name)
		}
		if name == t.Name() {
			tmpl = t
		} else {
			tmpl = t.New(name)
		}
		_, err = tmpl.Parse(s)
		if err != nil {
			return nil, err
		}
	}
	return t, nil
}

func GetTemplateNames() []string {
	files, _ := getFileSysNames(fs.FS(Files), ".")
	return files
}

func CompileTimeCheck(fsys fs.FS) error {
	files, err := getFileSysNames(fsys, ".")
	if err != nil {
		return err
	}
	template.Must(template.New("layout").Funcs(template.FuncMap(templateFuncs)).ParseFS(Files, files...))
	logger.Infof("compile time check completed")

	return nil
}

func getFileSysNames(fsys fs.FS, dirname string) ([]string, error) {
	entry, err := fs.ReadDir(fsys, dirname)
	if err != nil {
		return nil, fmt.Errorf("error reading embed directory, err: %w", err)
	}

	files := make([]string, 0, 100)
	for _, f := range entry {
		info, err := f.Info()
		if err != nil {
			return nil, fmt.Errorf("error returning file info err: %w", err)
		}
		if !f.IsDir() {
			files = append(files, filepath.Join(dirname, info.Name()))
		} else {
			names, err := getFileSysNames(fsys, info.Name())
			if err != nil {
				return nil, err
			}
			files = append(files, names...)
		}
	}

	return files, nil
}
