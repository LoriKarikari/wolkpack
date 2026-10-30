package main

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
)

func main() {
	// errors.AsType is new in Go 1.26, so this only builds on a Go newer than the go line in go.mod.
	_, err := os.Stat("/missing")
	_, missing := errors.AsType[*fs.PathError](err)
	http.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "hello from go, /missing is missing: %v", missing)
	})
	http.ListenAndServe(":"+os.Getenv("PORT"), nil)
}
