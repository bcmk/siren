package cmdlib

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"text/template"

	"gopkg.in/yaml.v3"
)

func translationFiles(t *testing.T) []string {
	t.Helper()
	pattern := "../../res/translations/*.yaml"
	files, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("failed to glob translation files: %v", err)
	}
	if len(files) == 0 {
		t.Fatalf("no translation files found matching %s", pattern)
	}
	return files
}

func TestTranslationsNoUnknownFields(t *testing.T) {
	files := translationFiles(t)
	for _, path := range files {
		t.Run(filepath.Base(path), func(t *testing.T) {
			file, err := os.Open(filepath.Clean(path))
			if err != nil {
				t.Fatalf("failed to open file: %v", err)
			}
			defer func() { _ = file.Close() }()

			decoder := yaml.NewDecoder(file)
			decoder.KnownFields(true)
			parsed := AllTranslations{}
			if err := decoder.Decode(&parsed); err != nil {
				t.Errorf("unknown or invalid fields in %s: %v", path, err)
			}
		})
	}
}

// translationCombinations returns all valid combinations of translation files
// grouped by common + endpoint-specific files.
func translationCombinations(t *testing.T) map[string][]string {
	t.Helper()
	files := translationFiles(t)
	commonFiles := map[string]string{}
	endpointFiles := map[string]map[string]string{}

	for _, path := range files {
		base := filepath.Base(path)
		// Skip ads files
		if len(base) >= 4 && base[:4] == "ads." {
			continue
		}
		// Parse filename: endpoint.lang.yaml or common.lang.yaml
		ext := filepath.Ext(base)
		nameWithoutExt := base[:len(base)-len(ext)]
		parts := splitLastDot(nameWithoutExt)
		if len(parts) != 2 {
			continue
		}
		name, lang := parts[0], parts[1]

		if name == "common" {
			commonFiles[lang] = path
		} else {
			if endpointFiles[name] == nil {
				endpointFiles[name] = map[string]string{}
			}
			endpointFiles[name][lang] = path
		}
	}

	result := map[string][]string{}
	for endpoint, langs := range endpointFiles {
		for lang, path := range langs {
			key := endpoint + "." + lang
			if common, ok := commonFiles[lang]; ok {
				result[key] = []string{common, path}
			} else {
				result[key] = []string{path}
			}
		}
	}
	return result
}

func splitLastDot(s string) []string {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '.' {
			return []string{s[:i], s[i+1:]}
		}
	}
	return []string{s}
}

func TestTranslationCombinationsLoad(t *testing.T) {
	combos := translationCombinations(t)
	if len(combos) == 0 {
		t.Fatal("no translation combinations found")
	}

	for name, files := range combos {
		t.Run(name, func(t *testing.T) {
			tr, allTr := LoadEndpointTranslations(files)
			if tr == nil {
				t.Error("LoadEndpointTranslations returned nil Translations")
			}
			if allTr == nil {
				t.Error("LoadEndpointTranslations returned nil AllTranslations")
			}
		})
	}
}

func TestTranslationCombinationsRequiredFields(t *testing.T) {
	combos := translationCombinations(t)

	for name, files := range combos {
		t.Run(name, func(t *testing.T) {
			tr, _ := LoadEndpointTranslations(files)
			rv := reflect.ValueOf(tr).Elem()
			for i := 0; i < rv.NumField(); i++ {
				field := rv.Field(i)
				tag := rv.Type().Field(i).Tag.Get("yaml")
				if field.IsNil() {
					t.Errorf("required field %q is nil", tag)
				}
			}
		})
	}
}

func TestTranslationTemplatesExecute(t *testing.T) {
	combos := translationCombinations(t)

	for name, files := range combos {
		t.Run(name, func(t *testing.T) {
			_, allTr := LoadEndpointTranslations(files)
			tpl := template.New("")
			tpl.Funcs(template.FuncMap{
				"mod": func(i, j int) int { return i % j },
				"add": func(i, j int) int { return i + j },
			})
			// affiliate_link is a dynamic template from config
			template.Must(tpl.New("affiliate_link").Parse("{{ . }}"))
			for k, v := range allTr {
				_, err := tpl.New(k).Parse(v.Str)
				if err != nil {
					t.Errorf("failed to parse template %q: %v", k, err)
				}
			}
			for k := range allTr {
				err := tpl.ExecuteTemplate(io.Discard, k, nil)
				if err != nil {
					t.Errorf("failed to execute template %q: %v", k, err)
				}
			}
		})
	}
}
