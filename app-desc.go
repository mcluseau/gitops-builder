package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"text/template"

	"github.com/go-git/go-git/v5/plumbing/object"
	"gopkg.in/yaml.v3"
)

type AppDesc struct {
	File     string
	Template string
	Data     map[string]any

	app *App
}

func (d AppDesc) GetApp(tree *object.Tree) (app App, err error) {
	if d.app != nil {
		app = *d.app
		return
	}

	read := func(path string) (ba []byte, err error) {
		f, err := tree.File(path)
		if err != nil {
			return
		}

		r, err := f.Reader()
		if err != nil {
			return
		}

		defer r.Close()

		ba, err = ioutil.ReadAll(r)
		return
	}

	var appBytes []byte

	switch {
	case d.File != "":
		appBytes, err = read(d.File)
		if err != nil {
			return
		}

	case d.Template != "":
		var tmplBytes []byte
		tmplBytes, err = read(d.Template)
		if err != nil {
			return
		}

		tmpl := template.New(d.Template)
		_, err = tmpl.Parse(string(tmplBytes))
		if err != nil {
			err = fmt.Errorf("failed to parse template %s: %w", d.Template, err)
			return
		}

		buf := new(bytes.Buffer)
		err = tmpl.Execute(buf, d.Data)
		if err != nil {
			err = fmt.Errorf("failed to render template %s: %w", d.Template, err)
			return
		}

		appBytes = buf.Bytes()
	}

	err = yaml.Unmarshal(appBytes, &app)
	if err != nil {
		return
	}

	d.app = &App{}
	*d.app = app
	return
}
