package main

// BSEItem maps the fields we need from the BSE API
type BSEItem struct {
	ScripCode   string `json:"scrip_Code"`
	ShortName   string `json:"short_name"`
	LongName    string `json:"Long_Name"`
	MeetingDate string `json:"meeting_date"`
	URL         string `json:"URL"`
}

// TrendItem maps relevant fields from Trendlyne search response
type TrendItem struct {
	ID              string `json:"id"`
	Label           string `json:"label"`
	Value           string `json:"value"`
	K               int    `json:"k"`
	SlugName        string `json:"slugname"`
	Country         string `json:"country"`
	DefaultExchange string `json:"defaultExchange"`
	BSEcode         string `json:"BSEcode"`
	NextURL         string `json:"nexturl"`
}

// QuarterValue is either a formatted number or "not declared"
type QuarterValue string

// CompanyResult holds the company and its last 4 quarter metrics
type CompanyResult struct {
	Company   string
	LongName  string
	Quarters  []string // names of the last 4 quarters (len up to 4)
	Revenue   []QuarterValue
	NetProfit []QuarterValue

	// Numeric versions for analysis. Use math.NaN() for missing/not-declared.
	RevenueNums   []float64
	NetProfitNums []float64
}
