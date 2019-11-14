package main

import (
	"encoding/json"
	"errors"
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
	OnlineList       *translation `json:"online_list"`
	Offline          *translation `json:"offline"`
	OfflineList      *translation `json:"offline_list"`
	Denied           *translation `json:"denied"`
	DeniedList       *translation `json:"denied_list"`
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
	NoOnlineModels   *translation `json:"no_online_models"`
	RemoveAll        *translation `json:"remove_all"`
	AllModelsRemoved *translation `json:"all_models_removed"`
}

func noNils(xs ...*translation) error {
	for _, x := range xs {
		if x == nil {
			return errors.New("required translation is not set")
		}
	}
	return nil
}

func loadTranslations(path string) translations {
	file, err := os.Open(filepath.Clean(path))
	checkErr(err)
	defer func() { checkErr(file.Close()) }()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	parsed := translations{}
	err = decoder.Decode(&parsed)
	checkErr(err)
	checkErr(noNils(
		parsed.Help,
		parsed.Online,
		parsed.OnlineList,
		parsed.Offline,
		parsed.OfflineList,
		parsed.Denied,
		parsed.DeniedList,
		parsed.SyntaxAdd,
		parsed.SyntaxRemove,
		parsed.SyntaxFeedback,
		parsed.InvalidSymbols,
		parsed.AlreadyAdded,
		parsed.MaxModels,
		parsed.AddError,
		parsed.ModelAdded,
		parsed.ModelNotInList,
		parsed.ModelRemoved,
		parsed.Donation,
		parsed.Feedback,
		parsed.SourceCode,
		parsed.UnknownCommand,
		parsed.Slash,
		parsed.Languages,
		parsed.Version,
		parsed.ProfileRemoved,
		parsed.NoModels,
		parsed.NoOnlineModels,
		parsed.RemoveAll,
		parsed.AllModelsRemoved,
	))
	return parsed
}
