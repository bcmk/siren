package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

//go:generate jsonenums -type=parseKind
type parseKind int

const (
	parseRaw parseKind = iota
	parseHTML
	parseMarkdown
)

func (r parseKind) String() string {
	switch r {
	case parseRaw:
		return "raw"
	case parseHTML:
		return "html"
	case parseMarkdown:
		return "markdown"
	}
	return "unknown"
}

type translation struct {
	Str   string    `json:"str"`
	Parse parseKind `json:"parse"`
}

type translations struct {
	Help             *translation `json:"help"`
	Online           *translation `json:"online"`
	Offline          *translation `json:"offline"`
	Denied           *translation `json:"denied"`
	SyntaxAdd        *translation `json:"syntax_add"`
	SyntaxRemove     *translation `json:"syntax_remove"`
	SyntaxFeedback   *translation `json:"syntax_feedback"`
	InvalidSymbols   *translation `json:"invalid_symbols"`
	AlreadyAdded     *translation `json:"already_added"`
	MaxModels        *translation `json:"max_models"`
	AddError         *translation `json:"add_error"`
	ModelAdded       *translation `json:"model_added"`
	ModelNotInList   *translation `json:"model_not_in_list"`
	ModelRemoved     *translation `json:"model_removed"`
	Donation         *translation `json:"donation"`
	Feedback         *translation `json:"feedback"`
	SourceCode       *translation `json:"source_code"`
	UnknownCommand   *translation `json:"unknown_command"`
	Slash            *translation `json:"slash"`
	Languages        *translation `json:"languages"`
	Version          *translation `json:"version"`
	ProfileRemoved   *translation `json:"profile_removed"`
	NoModels         *translation `json:"no_models"`
	RemoveAll        *translation `json:"remove_all"`
	AllModelsRemoved *translation `json:"all_models_removed"`
}

func loadTranslations(path string) translations {
	file, err := os.Open(filepath.Clean(path))
	checkErr(err)
	defer func() { checkErr(file.Close()) }()
	decoder := json.NewDecoder(file)
	parsed := translations{}
	err = decoder.Decode(&parsed)
	checkErr(err)
	return parsed
}
