// internal/output/quote.go
package output

import (
	"fmt"
	"math/rand"
)

var quotes = []string{
	"Works on my machine.  — Every developer, ever",
	"Le rideau se lève. May your configs be correct.",
	"git push to prod on a Friday. git push --force on a Friday.",
	"En scène. No turning back now.",
	"The show must go on — and so must the deploy.",
	"In staging we trust. In production we pray.",
	"Every deploy is a first night.",
	"Configuration is just code that hasn't been committed yet.",
}

// PrintOpeningQuote prints the theatrical opening quote (Level 2+ only).
func PrintOpeningQuote(level Level) {
	if level < Level2 {
		return
	}
	q := quotes[rand.Intn(len(quotes))]
	fmt.Printf("\n  %q\n\n", q)
}
