package middleware

import (
	"net/http"
	"os"
)

// noDirFS hides directories so the file server never lists folder contents.
type noDirFS struct{ http.FileSystem }

func (fs noDirFS) Open(name string) (http.File, error) {
	f, err := fs.FileSystem.Open(name)
	if err != nil {
		return nil, err
	}
	if st, err := f.Stat(); err != nil || st.IsDir() {
		f.Close()
		return nil, os.ErrNotExist
	}
	return f, nil
}

// StaticFiles serves uploaded files from dir (no directory listings).
func StaticFiles(dir string) http.Handler {
	fs := http.FileServer(noDirFS{http.Dir(dir)})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		fs.ServeHTTP(w, r)
	})
}
