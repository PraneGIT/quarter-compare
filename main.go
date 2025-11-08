package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// getOutputReportPath returns a dynamic path for report.html based on the user's system.
// Preferred location: $HOME/Documents/quarter-compare/report.html
// Fallbacks: executable directory, current working directory.
func getOutputReportPath() (string, error) {
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		dir := filepath.Join(home, "Documents", "quarter-compare")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
		return filepath.Join(dir, "report.html"), nil
	}
	// fallback: executable directory
	if exe, err2 := os.Executable(); err2 == nil {
		dir := filepath.Dir(exe)
		if err := os.MkdirAll(dir, 0o755); err == nil {
			return filepath.Join(dir, "report.html"), nil
		}
	}
	// final fallback: current working directory
	if wd, err3 := os.Getwd(); err3 == nil {
		return filepath.Join(wd, "report.html"), nil
	}
	return "report.html", nil
}

func main() {
	// enable more verbose logging (timestamp + file:line)
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	// create HTTP client with cookie jar
	client := NewHTTPClient()

	// 1. fetch BSE list
	bseURL := "https://api.bseindia.com/BseIndiaAPI/api/Corpforthresults/w"
	bseItems, err := FetchBSEList(client, bseURL)
	if err != nil {
		log.Fatalf("fetch bse list: %v", err)
	}

	// 2. filter by today's date
	today := time.Now().Format("02 Jan 2006")
	var todaysItems []BSEItem
	for _, it := range bseItems {
		if it.MeetingDate == today {
			todaysItems = append(todaysItems, it)
		}
	}
	if len(todaysItems) == 0 {
		fmt.Println("no meetings for today:", today)
		return
	}

	// 3. for each item, collect financials concurrently
	// concurrency limit (adjust as needed)
	const maxConcurrent = 20
	sem := make(chan struct{}, maxConcurrent)
	var wg sync.WaitGroup

	type result struct {
		cr  CompanyResult
		err error
	}
	resultsCh := make(chan result, len(todaysItems))

	for _, itm := range todaysItems {
		itm := itm // capture
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			log.Printf("processing (goroutine): %s %s", itm.ShortName, itm.LongName)

			// call trendlyne search
			trendItems, err := FetchTrendSearch(client, itm.ShortName)
			if err != nil {
				log.Printf("trend search error %s: %v", itm.ShortName, err)
				resultsCh <- result{err: err}
				return
			}
			if len(trendItems) == 0 {
				log.Printf("no trendlyne results for %s", itm.ShortName)
				resultsCh <- result{err: fmt.Errorf("no trendlyne results for %s", itm.ShortName)}
				return
			}
			// pick first matching entry
			tr := trendItems[0]

			// fetch trendlyne page to extract fundamentals URL
			pageURL := tr.NextURL
			if pageURL == "" {
				pageURL = fmt.Sprintf("https://trendlyne.com/equity/%d/%s/%s/", tr.K, tr.ID, tr.SlugName)
			}
			fundURL, err := ExtractFundamentalsURLFromPage(client, pageURL)
			if err != nil {
				log.Printf("extract fundamentals url failed for %s: %v", itm.ShortName, err)
				resultsCh <- result{err: err}
				return
			}

			// fetch fundamentals JSON
			fundJSON, err := FetchFundamentalsJSON(client, fundURL, pageURL)
			if err != nil {
				log.Printf("fetch fundamentals failed for %s: %v", itm.ShortName, err)
				resultsCh <- result{err: err}
				return
			}

			// parse and collect last 4 quarters
			cr := ParseCompanyFundamentals(itm.ShortName, fundJSON)
			// attach long name
			cr.LongName = itm.LongName
			resultsCh <- result{cr: cr, err: nil}
		}()
	}

	// wait for all workers and then close resultsCh
	go func() {
		wg.Wait()
		close(resultsCh)
	}()

	// gather results
	var results []CompanyResult
	for r := range resultsCh {
		if r.err != nil {
			// already logged inside worker; skip failed entry
			continue
		}
		results = append(results, r.cr)
	}

	// 4. generate HTML report
	outPath, err := getOutputReportPath()
	if err != nil {
		log.Fatalf("cannot determine output path: %v", err)
	}
	if err := GenerateHTMLReport(outPath, results); err != nil {
		log.Fatalf("generate report: %v", err)
	}
	fmt.Println("report saved to", outPath)
}
