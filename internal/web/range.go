package web

// statsRange — одно окно статистики и графика. Код из query (?range=3h).
type statsRange struct {
	Code      string
	Hours     int
	BucketSec int
	Short     string
	Title     string
}

var statsRanges = []statsRange{
	{Code: "1h", Hours: 1, BucketSec: 60, Short: "1ч", Title: "1 час"},
	{Code: "3h", Hours: 3, BucketSec: 3 * 60, Short: "3ч", Title: "3 часа"},
	{Code: "6h", Hours: 6, BucketSec: 5 * 60, Short: "6ч", Title: "6 часов"},
	{Code: "12h", Hours: 12, BucketSec: 10 * 60, Short: "12ч", Title: "12 часов"},
	{Code: "1d", Hours: 24, BucketSec: 15 * 60, Short: "1д", Title: "1 день"},
	{Code: "7d", Hours: 168, BucketSec: 3600, Short: "7д", Title: "7 дней"},
}

const defaultStatsRange = "1d"

func parseStatsRange(raw string) statsRange {
	for _, r := range statsRanges {
		if r.Code == raw {
			return r
		}
	}
	return statsRangeByCode(defaultStatsRange)
}

func statsRangeByCode(code string) statsRange {
	for _, r := range statsRanges {
		if r.Code == code {
			return r
		}
	}
	return statsRanges[4] // 1d
}

// chartTimeLayout: короткий интервал — только время, длинный — дата и час.
func (r statsRange) chartTimeLayout() string {
	if r.Hours <= 3 {
		return "15:04"
	}
	return "02.01 15:04"
}
