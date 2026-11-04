package database

import (
	"slices"
	"strings"
	"unicode"
)

// SanitizeQuery replaces literal values in SQL with ?, so db.query.text can be
// recorded without leaking data or creating a new value per query. Strings and
// numbers become ?, and comments are dropped.
//
// SQL dialects disagree about where a literal ends, and server settings change
// the answer (MySQL's NO_BACKSLASH_ESCAPES, SQL Server's QUOTED_IDENTIFIER). A
// wrong guess leaves the tokenizer out of step, exposing what follows, so it
// doesn't guess. Anything that dialects read differently ends the scan and the
// rest of the query becomes ?:
//
//   - a backslash inside any quoted text
//   - a quote inside a comment or inside [brackets], and a nested /*
//   - an Oracle q'[…]' literal
//   - anything unterminated
//
// Queries using those lose detail, and no query is safe to log just because it
// passed through here: keep secrets in bind parameters. system is the
// db.system.name value. "…" stays as an identifier only where that is reliably
// what it means; everywhere else it is treated as a string.
//
//nolint:gocyclo // a tokenizer: one switch case per kind of token
func SanitizeQuery(sql, system string) string {
	system = strings.ToLower(system)
	mysql := system == "mysql" || system == "mariadb"
	postgres := slices.Contains([]string{"postgresql", "postgres", "cockroachdb", "aws.redshift"}, system)
	quotedIdentifiers := postgres || slices.Contains([]string{"oracle", "db2", "ibm.db2"}, system)
	numberedParams := postgres || system == "sqlite"

	chars := []rune(sql)
	out := make([]rune, 0, len(chars))
	hideRest := func() string {
		out = append(out, '?')
		return strings.Join(strings.Fields(string(out)), " ")
	}

	for i := 0; i < len(chars); {
		c := chars[i]
		inWord := len(out) > 0 && isWord(out[len(out)-1])
		last := i // the last character this step consumes

		switch {
		case c == '\'':
			if hasOraclePrefix(out) {
				return hideRest()
			}
			end, ok := quotedEnd(chars, i)
			if !ok {
				return hideRest()
			}
			if postgres && hasEscapePrefix(out) {
				out = out[:len(out)-1] // E'…' is one literal, so one ?
			}
			out = append(out, '?')
			last = end

		case c == '"' && !quotedIdentifiers:
			end, ok := quotedEnd(chars, i)
			if !ok {
				return hideRest()
			}
			out = append(out, '?')
			last = end

		case c == '"' || c == '`':
			end, ok := quotedEnd(chars, i)
			if !ok {
				return hideRest()
			}
			out = append(out, chars[i:end+1]...)
			last = end

		case c == '[':
			end := slices.Index(chars[i:], ']')
			if end < 0 || containsQuote(chars[i:i+end]) {
				return hideRest()
			}
			out = append(out, c)

		case c == '$' && !inWord && !mysql:
			if isASCIIDigit(at(chars, i+1)) {
				if numberedParams {
					out = append(out, c)
					break
				}
				last = numberEnd(chars, i+1)
				out = append(out, '?')
				break
			}
			end, state := dollarQuoteEnd(chars, i)
			switch state {
			case dollarLiteral:
				out = append(out, '?')
				last = end
			case dollarUnterminated:
				return hideRest()
			case dollarNotAQuote:
				out = append(out, c)
			}

		case c == '-' && at(chars, i+1) == '-', c == '#' && mysql:
			end := lineEnd(chars, i)
			// MySQL needs a space after --, so 5--1 may not be a comment at all.
			if containsQuote(chars[i : end+1]) {
				return hideRest()
			}
			last = end

		case c == '/' && at(chars, i+1) == '*':
			end := commentEnd(chars, i)
			if end < 0 || nestedComment(chars[i+2:end]) {
				return hideRest()
			}
			out = append(out, ' ')
			last = end

		case isASCIIDigit(c) && !inWord:
			last = numberEnd(chars, i)
			out = append(out, '?')

		default:
			out = append(out, c)
		}
		i = last + 1
	}

	return strings.Join(strings.Fields(string(out)), " ")
}

// at returns the character at i, or 0 past the end.
func at(chars []rune, i int) rune {
	if i < len(chars) {
		return chars[i]
	}
	return 0
}

func isWord(c rune) bool {
	return unicode.IsLetter(c) || unicode.IsNumber(c) || c == '_' || c == '$'
}

func isASCIIDigit(c rune) bool { return c >= '0' && c <= '9' }

func isASCIIAlnum(c rune) bool {
	return isASCIIDigit(c) || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isQuote(c rune) bool { return c == '\'' || c == '"' || c == '`' }

func containsQuote(chars []rune) bool { return slices.ContainsFunc(chars, isQuote) }

// numberEnd is the index of the last character of the number at start, covering
// 0x1F, 1e3, 9.99 and 4111_1111.
func numberEnd(chars []rune, start int) int {
	last := start
	for next := at(chars, last+1); isASCIIAlnum(next) || next == '.' || next == '_'; next = at(chars, last+1) {
		last++
	}
	return last
}

// standaloneSuffix reports whether out ends in word, not as part of a longer one.
func standaloneSuffix(out []rune, word string) bool {
	n := len(word)
	if len(out) < n || !strings.EqualFold(string(out[len(out)-n:]), word) {
		return false
	}
	return len(out) == n || !isWord(out[len(out)-n-1])
}

func hasEscapePrefix(out []rune) bool { return standaloneSuffix(out, "e") }

func hasOraclePrefix(out []rune) bool {
	return standaloneSuffix(out, "q") || standaloneSuffix(out, "nq")
}

// quotedEnd finds the closing quote for the text opening at start, with doubled
// quotes as escapes. A backslash inside counts as not finding it: whether it
// escapes the next quote depends on the dialect and its settings.
func quotedEnd(chars []rune, start int) (int, bool) {
	quote := chars[start]
	for i := start + 1; i < len(chars); i++ {
		switch c := chars[i]; {
		case c == '\\':
			return 0, false
		case c == quote && at(chars, i+1) == quote:
			i++
		case c == quote:
			return i, true
		}
	}
	return 0, false
}

// lineEnd is the index of the line's last character, so a comment is skipped and
// its newline kept.
func lineEnd(chars []rune, start int) int {
	for k := start; k < len(chars); k++ {
		if chars[k] == '\n' {
			return k - 1
		}
	}
	return len(chars) - 1
}

// commentEnd is the index of the / closing the block comment at start, or -1.
func commentEnd(chars []rune, start int) int {
	for k := start + 2; k+1 < len(chars); k++ {
		if chars[k] == '*' && chars[k+1] == '/' {
			return k + 1
		}
	}
	return -1
}

// nestedComment reports a /* inside a comment: PostgreSQL and SQL Server nest
// them, so the comment may not end where it seems to.
func nestedComment(body []rune) bool {
	return strings.Contains(string(body), "/*")
}

type dollarState int

const (
	dollarNotAQuote dollarState = iota
	dollarLiteral
	dollarUnterminated
)

// dollarQuoteEnd reads Postgres dollar quoting, $$…$$ or $tag$…$tag$, and returns
// the index of the closing $. Tags may be any letters, not just ASCII.
func dollarQuoteEnd(chars []rune, start int) (int, dollarState) {
	isTag := func(c rune) bool { return unicode.IsLetter(c) || unicode.IsNumber(c) || c == '_' }
	tagEnd := start + 1
	for isTag(at(chars, tagEnd)) {
		tagEnd++
	}
	if at(chars, tagEnd) != '$' {
		return 0, dollarNotAQuote
	}

	delimiter := chars[start : tagEnd+1]
	for k := tagEnd + 1; k+len(delimiter) <= len(chars); k++ {
		if slices.Equal(chars[k:k+len(delimiter)], delimiter) {
			return k + len(delimiter) - 1, dollarLiteral
		}
	}
	return 0, dollarUnterminated
}
