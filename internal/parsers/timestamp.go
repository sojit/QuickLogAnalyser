package parsers

import "quickanalyser/internal/timeparse"

func timeparseExtract(line string) string {
	return timeparse.ExtractRaw(line)
}
