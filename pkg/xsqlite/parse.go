package xsqlite

import (
	"errors"
	"fmt"
)

type parseAllColumnsState int

const (
	parseAllColumnsStateNone parseAllColumnsState = iota
	parseAllColumnsStateBeginning
	parseAllColumnsStateReadingRawName
	parseAllColumnsStateReadingQuotedName
	parseAllColumnsStateEndOfName
	parseAllColumnsStateStateEnd
)

func (m *Migrator) parseAllColumns(in string) ([]string, error) {
	s := []rune(in)
	columns := make([]string, 0)
	state := parseAllColumnsStateNone
	quote := rune(0)
	name := make([]rune, 0)
	for i := 0; i < len(s); i++ {
		switch state {
		case parseAllColumnsStateNone:
			if s[i] == '(' {
				state = parseAllColumnsStateBeginning
			}
		case parseAllColumnsStateBeginning:
			if m.isSpace(s[i]) {
				continue
			}
			if m.isQuote(s[i]) {
				state = parseAllColumnsStateReadingQuotedName
				quote = s[i]
				continue
			}
			if s[i] == '[' {
				state = parseAllColumnsStateReadingQuotedName
				quote = ']'
				continue
			} else if s[i] == ')' {
				return columns, fmt.Errorf("unexpected token: %s", string(s[i]))
			}
			state = parseAllColumnsStateReadingRawName
			name = append(name, s[i])
		case parseAllColumnsStateReadingRawName:
			if m.isSeparator(s[i]) {
				state = parseAllColumnsStateBeginning
				columns = append(columns, string(name))
				name = make([]rune, 0)
				continue
			}
			if s[i] == ')' {
				state = parseAllColumnsStateStateEnd
				columns = append(columns, string(name))
			}
			if m.isQuote(s[i]) {
				return nil, fmt.Errorf("unexpected token: %s", string(s[i]))
			}
			if m.isSpace(s[i]) {
				state = parseAllColumnsStateEndOfName
				columns = append(columns, string(name))
				name = make([]rune, 0)
				continue
			}
			name = append(name, s[i])
		case parseAllColumnsStateReadingQuotedName:
			if s[i] == quote {
				// check if quote character is escaped
				if i+1 < len(s) && s[i+1] == quote {
					name = append(name, quote)
					i++
					continue
				}
				state = parseAllColumnsStateEndOfName
				columns = append(columns, string(name))
				name = make([]rune, 0)
				continue
			}
			name = append(name, s[i])
		case parseAllColumnsStateEndOfName:
			if m.isSpace(s[i]) {
				continue
			}
			if m.isSeparator(s[i]) {
				state = parseAllColumnsStateBeginning
				continue
			}
			if s[i] == ')' {
				state = parseAllColumnsStateStateEnd
				continue
			}
			return nil, fmt.Errorf("unexpected token: %s", string(s[i]))
		case parseAllColumnsStateStateEnd:
			break
		}
	}
	if state != parseAllColumnsStateStateEnd {
		return nil, errors.New("unexpected end")
	}
	return columns, nil
}

func (m *Migrator) isSpace(r rune) bool {
	return r == ' ' || r == '\t'
}

func (m *Migrator) isQuote(r rune) bool {
	return r == '`' || r == '"' || r == '\''
}

func (m *Migrator) isSeparator(r rune) bool {
	return r == ','
}
