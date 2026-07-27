package loader

import (
	"database/sql"
)

// SpanishFreqLoader loads frequency data for Spanish words. There is no
// freely redistributable equivalent of SUBTLEX-US or CETEMPúblico for Spanish
// with a stable download URL, so this uses the OpenSubtitles-derived list from
// hermitdave/FrequencyWords, with hand-placed TSVs taking precedence.
type SpanishFreqLoader struct{}

func (SpanishFreqLoader) Name() string { return "es-freq" }

func (s SpanishFreqLoader) Load(db *sql.DB, dataDir string) error {
	return FrequencyListLoader{
		LoaderName: s.Name(),
		Lang:       "es",
		Files: []string{
			"es-freq.tsv",
			"es_ES-freq.tsv",
			"es_50k.txt",  // hermitdave/FrequencyWords
			"es_full.txt", // hermitdave/FrequencyWords
		},
	}.Load(db, dataDir)
}
