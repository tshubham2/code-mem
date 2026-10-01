package index

import "strings"

var stopwords = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`a an and are as at be but by do does for from has have
		how if in into is it its no not of on or so than that the then there these this
		to was we were what when where which who why will with without you your our`) {
		stopwords[w] = true
	}
}

// Tokenize lowercases text and splits it into BM25 terms. Identifiers keep
// their whole form and also contribute their parts, so `search_path`
// matches both "search_path" and "path". Light plural folding makes
// "policies" match "policy".
func Tokenize(text string) []string {
	var out []string
	emit := func(w string) {
		if len(w) < 2 || len(w) > 64 || stopwords[w] {
			return
		}
		out = append(out, fold(w))
	}
	for _, word := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !(r == '_' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r > 127)
	}) {
		word = strings.Trim(word, "_")
		emit(word)
		if strings.Contains(word, "_") {
			for _, part := range strings.Split(word, "_") {
				emit(part)
			}
		}
	}
	return out
}

func fold(w string) string {
	switch {
	case len(w) > 4 && strings.HasSuffix(w, "ies"):
		return w[:len(w)-3] + "y"
	case len(w) > 3 && strings.HasSuffix(w, "s") &&
		!strings.HasSuffix(w, "ss") && !strings.HasSuffix(w, "us") && !strings.HasSuffix(w, "is"):
		return w[:len(w)-1]
	}
	return w
}
