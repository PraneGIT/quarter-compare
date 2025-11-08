package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/http/cookiejar"
	"regexp"
	"strconv"
	"strings"
)

// NewHTTPClient returns an http.Client with cookie jar
func NewHTTPClient() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar}
}

// FetchBSEList fetches the BSE API and unmarshals it
func FetchBSEList(client *http.Client, url string) ([]BSEItem, error) {
	req, _ := http.NewRequest("GET", url, nil)
	// stronger browser-like headers to reduce HTML error pages
	req.Header.Set("accept", "application/json, text/plain, */*")
	req.Header.Set("accept-language", "en-US,en;q=0.7")
	req.Header.Set("origin", "https://www.bseindia.com")
	req.Header.Set("referer", "https://www.bseindia.com/")
	req.Header.Set("user-agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/142.0.0.0 Safari/537.36")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// if server returned HTML (starts with '<' or content-type is html), attempt to recover
	ct := resp.Header.Get("Content-Type")
	trimmed := bytes.TrimSpace(b)
	if len(trimmed) == 0 {
		return nil, errors.New("empty response from BSE endpoint")
	}
	if strings.HasPrefix(string(trimmed), "<") || strings.Contains(strings.ToLower(ct), "text/html") {
		// try to find JSON inside the HTML (first '{' or '[')
		jsonb, err2 := extractJSONFromBody(trimmed)
		if err2 != nil {
			// helpful debug info for future troubleshooting
			snippet := string(trimmed)
			if len(snippet) > 512 {
				snippet = snippet[:512]
			}
			return nil, fmt.Errorf("response appears to be HTML and no JSON found. status=%d snippet=%q", resp.StatusCode, snippet)
		}
		b = jsonb
	}

	var items []BSEItem
	if err := json.Unmarshal(b, &items); err != nil {
		// if still failing, provide a snippet to help debugging
		snippet := string(b)
		if len(snippet) > 512 {
			snippet = snippet[:512]
		}
		return nil, fmt.Errorf("invalid JSON from BSE endpoint: %v snippet=%q", err, snippet)
	}
	return items, nil
}

// extractJSONFromBody looks for the first '{' or '[' and returns bytes from that position to end,
// trimming any trailing HTML after matching JSON object/array using a lightweight balance scan.
func extractJSONFromBody(b []byte) ([]byte, error) {
	// find first '{' or '['
	idxObj := bytes.IndexByte(b, '{')
	idxArr := bytes.IndexByte(b, '[')
	start := -1
	if idxObj == -1 {
		start = idxArr
	} else if idxArr == -1 {
		start = idxObj
	} else {
		if idxObj < idxArr {
			start = idxObj
		} else {
			start = idxArr
		}
	}
	if start == -1 {
		return nil, errors.New("no JSON start delimiter found")
	}

	// find matching end by simple bracket balance (works for well-formed JSON)
	open := b[start]
	var close byte
	if open == '{' {
		close = '}'
	} else {
		close = ']'
	}

	depth := 0
	inString := false
	escapeNext := false
	for i := start; i < len(b); i++ {
		c := b[i]
		if inString {
			if escapeNext {
				escapeNext = false
			} else {
				if c == '\\' {
					escapeNext = true
				} else if c == '"' {
					inString = false
				}
			}
			continue
		}
		if c == '"' {
			inString = true
			continue
		}
		if c == open {
			depth++
			continue
		}
		if c == close {
			depth--
			if depth == 0 {
				// include this char and return slice up to here
				return bytes.TrimSpace(b[start : i+1]), nil
			}
		}
	}
	return nil, errors.New("could not find matching JSON end")
}

// FetchTrendSearch calls trendlyne autocomplete and returns parsed items
func FetchTrendSearch(client *http.Client, term string) ([]TrendItem, error) {
	esc := term
	url := fmt.Sprintf("https://trendlyne.com/member/api/ac_snames/all/?term=%s&all-results=true", esc)
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("accept", "*/*")
	req.Header.Set("referer", "https://trendlyne.com/")
	req.Header.Set("user-agent", "go-client")
	req.Header.Set("x-requested-with", "XMLHttpRequest")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var items []TrendItem
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		// some endpoints return HTML/error; return empty slice on parse error
		return nil, err
	}
	return items, nil
}

// ExtractFundamentalsURLFromPage fetches HTML page and finds data-tablesurl
func ExtractFundamentalsURLFromPage(client *http.Client, pageURL string) (string, error) {
	req, _ := http.NewRequest("GET", pageURL, nil)
	req.Header.Set("user-agent", "go-client")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	// first try the original data-tablesurl attribute
	re := regexp.MustCompile(`data-tablesurl=(https?://[^\s"'<>]+)`)
	m := re.FindSubmatch(body)
	if len(m) >= 2 {
		url := string(m[1])
		return url, nil
	}
	// try quoted attribute variant
	re2 := regexp.MustCompile(`data-tablesurl\s*=\s*["'](https?://[^"']+)["']`)
	m2 := re2.FindSubmatch(body)
	if len(m2) >= 2 {
		return string(m2[1]), nil
	}

	// fallback: search for any URL that contains get-fundamental_results (more robust)
	reGet := regexp.MustCompile(`https?://[^\s"'<>]*get-fundamental_results[^\s"'<>]*`)
	m3 := reGet.Find(body)
	if m3 != nil {
		// normalize: ensure trailing slash (Trendlyne seems to expect a trailing slash in examples)
		u := string(bytes.TrimSpace(m3))
		if !strings.HasSuffix(u, "/") {
			u = u + "/"
		}
		log.Printf("ExtractFundamentalsURLFromPage: fallback found fundamentals URL=%s", u)
		return u, nil
	}

	// no URL found
	return "", errors.New("data-tablesurl not found")
}

// FetchFundamentalsJSON GETs the fundamentals URL and returns raw JSON bytes
func FetchFundamentalsJSON(client *http.Client, fundURL, referer string) ([]byte, error) {
	req, _ := http.NewRequest("GET", fundURL, nil)
	req.Header.Set("accept", "*/*")
	req.Header.Set("referer", referer)
	req.Header.Set("user-agent", "go-client")
	req.Header.Set("x-requested-with", "XMLHttpRequest")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	// log status for diagnostics
	log.Printf("FetchFundamentalsJSON: url=%s status=%d len=%d", fundURL, resp.StatusCode, len(b))

	// ensure it's JSON
	clean := bytes.TrimSpace(b)
	if len(clean) == 0 {
		return nil, errors.New("empty fundamentals response")
	}
	// quick check and log snippet if not JSON-like
	if !(clean[0] == '{' || clean[0] == '[') {
		snippet := string(clean)
		if len(snippet) > 512 {
			snippet = snippet[:512]
		}
		log.Printf("FetchFundamentalsJSON: response does not start with JSON token for %s snippet=%q", fundURL, snippet)
		// still return the content; caller can attempt to recover or fail
	}
	return clean, nil
}

// ParseCompanyFundamentals extracts last 4 quarters revenue and net profit
func ParseCompanyFundamentals(shortName string, fundJSON []byte) CompanyResult {
	log.Printf("ParseCompanyFundamentals: start for %s (bytes=%d)", shortName, len(fundJSON))
	cr := CompanyResult{
		Company: shortName,
	}
	// decode into map
	var root map[string]interface{}
	if err := json.Unmarshal(fundJSON, &root); err != nil {
		log.Printf("ParseCompanyFundamentals: json unmarshal error for %s: %v", shortName, err)
		return cr
	}
	body, _ := root["body"].(map[string]interface{})
	if body == nil {
		log.Printf("ParseCompanyFundamentals: no body in fundamentals JSON for %s", shortName)
	}
	qOrder := []string{}
	if body != nil {
		if qo, ok := body["quarterlyOrder"].([]interface{}); ok {
			for _, qi := range qo {
				if s, ok := qi.(string); ok {
					qOrder = append(qOrder, s)
				}
			}
		}
	}
	if len(qOrder) == 0 {
		log.Printf("ParseCompanyFundamentals: quarterlyOrder empty for %s", shortName)
	}

	// choose best dump map (prefer consolidated if it contains the quarter keys; else pick best match)
	var dump map[string]interface{}
	if body != nil {
		if qdRaw, ok := body["quarterlyDataDump"]; ok {
			if qd, ok := qdRaw.(map[string]interface{}); ok {
				// pick the best candidate among entries of qd (consolidated/standalone/others)
				dump = chooseBestDump(qd, qOrder)
				if dump == nil {
					log.Printf("ParseCompanyFundamentals: no suitable quarterlyDataDump candidate found for %s; will attempt best-effort reads", shortName)
				}
			} else {
				log.Printf("ParseCompanyFundamentals: quarterlyDataDump has unexpected type for %s", shortName)
			}
		} else {
			log.Printf("ParseCompanyFundamentals: no quarterlyDataDump for %s", shortName)
		}
	}
	if dump == nil {
		log.Printf("ParseCompanyFundamentals: consolidated dump not found for %s", shortName)
	}

	// helper: find best matching key in dump for requested quarter label
	findQuarterKey := func(d map[string]interface{}, q string) string {
		normalize := func(s string) string {
			s = strings.ToLower(s)
			// remove all non-alphanumeric chars
			re := regexp.MustCompile(`[^a-z0-9]`)
			return re.ReplaceAllString(s, "")
		}
		nq := normalize(q)
		// exact-normalized match first
		for k := range d {
			if nq == normalize(k) {
				return k
			}
		}
		// containment heuristics
		for k := range d {
			nk := normalize(k)
			if strings.Contains(nk, nq) || strings.Contains(nq, nk) {
				return k
			}
		}
		return ""
	}

	// take up to first 4 quarters from qOrder
	max := 4
	if len(qOrder) < 4 {
		max = len(qOrder)
	}
	cr.Quarters = make([]string, 0, 4)
	cr.Revenue = make([]QuarterValue, 0, 4)
	cr.NetProfit = make([]QuarterValue, 0, 4)
	for i := 0; i < max; i++ {
		q := qOrder[i]
		cr.Quarters = append(cr.Quarters, q)
		if dump != nil {
			// try direct key
			if qmap, ok := dump[q].(map[string]interface{}); ok {
				rev := valueFromMap(qmap, "TOTAL_SR_Q", "SR_Q")
				np := valueFromMap(qmap, "NP_Q")
				if string(rev) == "not declared" {
					log.Printf("ParseCompanyFundamentals: revenue keys missing for %s quarter=%s keys=[TOTAL_SR_Q,SR_Q]", shortName, q)
				}
				if string(np) == "not declared" {
					log.Printf("ParseCompanyFundamentals: netprofit key missing for %s quarter=%s key=[NP_Q]", shortName, q)
				}
				cr.Revenue = append(cr.Revenue, rev)
				cr.NetProfit = append(cr.NetProfit, np)
				continue
			}
			// try fuzzy match on keys
			if alt := findQuarterKey(dump, q); alt != "" {
				if qmap, ok := dump[alt].(map[string]interface{}); ok {
					log.Printf("ParseCompanyFundamentals: matched quarter %s -> dump key %s for %s", q, alt, shortName)
					rev := valueFromMap(qmap, "TOTAL_SR_Q", "SR_Q")
					np := valueFromMap(qmap, "NP_Q")
					cr.Revenue = append(cr.Revenue, rev)
					cr.NetProfit = append(cr.NetProfit, np)
					continue
				}
			}
			// quarter entry missing inside dump
			log.Printf("ParseCompanyFundamentals: quarter %s missing in dump for %s", q, shortName)
		} else {
			// dump is nil
			log.Printf("ParseCompanyFundamentals: no dump to read quarter %s for %s", q, shortName)
		}
		// not found
		cr.Revenue = append(cr.Revenue, QuarterValue("not declared"))
		cr.NetProfit = append(cr.NetProfit, QuarterValue("not declared"))
	}
	// pad up to 4 entries with "not declared"
	for len(cr.Quarters) < 4 {
		cr.Quarters = append(cr.Quarters, "")
		cr.Revenue = append(cr.Revenue, QuarterValue("not declared"))
		cr.NetProfit = append(cr.NetProfit, QuarterValue("not declared"))
	}
	log.Printf("ParseCompanyFundamentals: finished for %s quarters=%v revenue=%v netprofit=%v", shortName, cr.Quarters, cr.Revenue, cr.NetProfit)

	// populate numeric arrays (NaN for "not declared")
	cr.RevenueNums = make([]float64, len(cr.Revenue))
	cr.NetProfitNums = make([]float64, len(cr.NetProfit))
	for i := 0; i < len(cr.Revenue); i++ {
		cr.RevenueNums[i] = quarterValueToFloat64(cr.Revenue[i])
		cr.NetProfitNums[i] = quarterValueToFloat64(cr.NetProfit[i])
	}

	return cr
}

// quarterValueToFloat64 converts QuarterValue to float64, returns NaN if not parseable
func quarterValueToFloat64(q QuarterValue) float64 {
	s := strings.TrimSpace(string(q))
	if s == "" || strings.EqualFold(s, "not declared") {
		return math.NaN()
	}
	// remove commas if any
	s = strings.ReplaceAll(s, ",", "")
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	return math.NaN()
}

// chooseBestDump scores candidates under quarterlyDataDump and returns the map with most matches
func chooseBestDump(qd map[string]interface{}, qOrder []string) map[string]interface{} {
	normalize := func(s string) string {
		s = strings.ToLower(s)
		re := regexp.MustCompile(`[^a-z0-9]`)
		return re.ReplaceAllString(s, "")
	}
	// prepare normalized targets
	targets := make([]string, 0, len(qOrder))
	for _, q := range qOrder {
		targets = append(targets, normalize(q))
	}
	bestKey := ""
	bestScore := -1
	var bestMap map[string]interface{}
	for k, v := range qd {
		m, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		// score how many targets appear in m's keys (normalized)
		score := 0
		for mk := range m {
			nmk := normalize(mk)
			for _, t := range targets {
				if t == nmk || strings.Contains(nmk, t) || strings.Contains(t, nmk) {
					score++
					// don't double-count this mk for other targets
					break
				}
			}
		}
		log.Printf("chooseBestDump: candidate=%s score=%d keys=%d", k, score, len(m))
		if score > bestScore {
			bestScore = score
			bestKey = k
			bestMap = m
		}
	}
	if bestMap != nil {
		log.Printf("chooseBestDump: selected candidate=%s with score=%d", bestKey, bestScore)
	}
	return bestMap
}

// valueFromMap tries keys in order and returns formatted QuarterValue
func valueFromMap(m map[string]interface{}, keys ...string) QuarterValue {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			switch vv := v.(type) {
			case float64:
				return QuarterValue(formatFloat(vv))
			case string:
				// sometimes numbers are strings
				if f, err := strconv.ParseFloat(vv, 64); err == nil {
					return QuarterValue(formatFloat(f))
				}
				if vv == "" {
					continue
				}
				return QuarterValue(vv)
			case int:
				return QuarterValue(formatFloat(float64(vv)))
			default:
				// try marshal -> string
				b, _ := json.Marshal(vv)
				if len(b) > 0 {
					return QuarterValue(string(b))
				}
			}
		}
	}
	return QuarterValue("not declared")
}

// formatFloat with 2 decimals and trim .00 if integer-like
func formatFloat(f float64) string {
	// show up to 2 decimals, trim trailing zeros
	s := strconv.FormatFloat(f, 'f', 2, 64)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	return s
}
