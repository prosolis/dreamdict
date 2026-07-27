package loader

import (
	"database/sql"
)

// CETEMPublicoLoader loads frequency data for European Portuguese words.
// Accepts a simple word-frequency TSV file derived from the CETEMPúblico corpus
// or any frequency list in "word<tab>count" or "word<tab>frequency_per_million" format.
type CETEMPublicoLoader struct{}

func (CETEMPublicoLoader) Name() string { return "cetempublico" }

func (c CETEMPublicoLoader) Load(db *sql.DB, dataDir string) error {
	return FrequencyListLoader{
		LoaderName: c.Name(),
		Lang:       "pt-PT",
		Files: []string{
			"cetempublico-freq.tsv",
			"pt-freq.tsv",
			"pt_PT-freq.tsv",
			"pt_50k.txt",  // hermitdave/FrequencyWords
			"pt_full.txt", // hermitdave/FrequencyWords
		},
	}.Load(db, dataDir)
}
