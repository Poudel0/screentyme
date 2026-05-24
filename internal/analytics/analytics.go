package analytics

import (
	"context"
	"database/sql"
	"fmt"
)

type Analyzer struct {
	db *sql.DB
}

func New(db *sql.DB) *Analyzer {
	return &Analyzer{db: db}
}

// AppTotal holds per-app aggregation for some time window.
type AppTotal struct {
	AppClass       string `json:"app_class"`
	TotalSeconds   int    `json:"total_seconds"`
	SessionCount   int    `json:"session_count"`
	LongestSession int    `json:"longest_session_seconds"`
}

// TodayByApp returns mutually-exclusive app totals for the current local day.
func (a *Analyzer) TodayByApp(ctx context.Context) ([]AppTotal, error) {
	const q = `
	WITH
	bounds AS (
		SELECT
			CAST(strftime('%s', date('now','localtime'),'utc')        AS INTEGER) AS start_ts,
			CAST(strftime('%s', date('now','localtime','+1 day'),'utc') AS INTEGER) AS end_ts
	),
	gapped AS (
		SELECT
			ts, app_class,
			ts - LAG(ts) OVER (PARTITION BY app_class ORDER BY ts) AS gap
		FROM samples, bounds
		WHERE ts >= bounds.start_ts AND ts < bounds.end_ts
	),
	flagged AS (
		SELECT ts, app_class,
			CASE WHEN gap IS NULL OR gap > 30 THEN 1 ELSE 0 END AS is_new_session
		FROM gapped
	),
	sessioned AS (
		SELECT ts, app_class,
			SUM(is_new_session) OVER (
				PARTITION BY app_class ORDER BY ts
				ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW
			) AS session_id
		FROM flagged
	),
	sessions AS (
		SELECT app_class, session_id,
			MAX(ts) - MIN(ts) + 5 AS duration_seconds
		FROM sessioned
		GROUP BY app_class, session_id
	)
	SELECT
		app_class,
		SUM(duration_seconds)  AS total_seconds,
		COUNT(*)               AS session_count,
		MAX(duration_seconds)  AS longest_session
	FROM sessions
	WHERE duration_seconds >= 10
	GROUP BY app_class
	ORDER BY total_seconds DESC
	`
	rows, err := a.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("today by app: %w", err)
	}
	defer rows.Close()

	results := []AppTotal{}
	for rows.Next() {
		var r AppTotal
		if err := rows.Scan(&r.AppClass, &r.TotalSeconds, &r.SessionCount, &r.LongestSession); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// KeywordTotal holds per-(app_class, keyword) aggregation.
// When Keyword is empty the row is unmatched; Title holds the window title
// so the user can decide whether to track it.
type KeywordTotal struct {
	AppClass     string `json:"app_class"`
	Keyword      string `json:"keyword"`
	Label        string `json:"label"`
	Title        string `json:"title,omitempty"`
	TotalSeconds int    `json:"total_seconds"`
	SessionCount int    `json:"session_count,omitempty"`
}

// RecentByKeyword returns keyword-lens totals for the last `days` local days
// (e.g. days=7 covers today + 6 prior days). Unmatched titles are included so
// the user can discover what to track next.
func (a *Analyzer) RecentByKeyword(ctx context.Context, days int) ([]KeywordTotal, error) {
	const q = `
	WITH
	bounds AS (
		SELECT
			CAST(strftime('%s', date('now','localtime', ?),'utc')       AS INTEGER) AS start_ts,
			CAST(strftime('%s', date('now','localtime','+1 day'),'utc') AS INTEGER) AS end_ts
	),
	recent AS (
		SELECT ts, app_class, title
		FROM samples, bounds
		WHERE ts >= bounds.start_ts AND ts < bounds.end_ts
		  AND title IS NOT NULL
	),
	matched AS (
		SELECT
			r.ts,
			r.app_class,
			r.title,
			k.keyword,
			COALESCE(k.label, k.keyword) AS label
		FROM recent r
		LEFT JOIN tracked_keywords k
			ON k.app_class = r.app_class
			AND r.title LIKE '%' || k.keyword || '%'
	),
	gapped AS (
		SELECT
			ts, app_class, title, keyword, label,
			ts - LAG(ts) OVER (
				PARTITION BY app_class, COALESCE(keyword, title)
				ORDER BY ts
			) AS gap
		FROM matched
	),
	flagged AS (
		SELECT *,
			CASE WHEN gap IS NULL OR gap > 30 THEN 1 ELSE 0 END AS is_new_session
		FROM gapped
	),
	sessioned AS (
		SELECT *,
			SUM(is_new_session) OVER (
				PARTITION BY app_class, COALESCE(keyword, title)
				ORDER BY ts
				ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW
			) AS session_id
		FROM flagged
	),
	sessions AS (
		SELECT
			app_class, keyword, label, title, session_id,
			MAX(ts) - MIN(ts) + 5 AS duration_seconds
		FROM sessioned
		GROUP BY app_class, COALESCE(keyword, title), session_id
	)
	SELECT
		app_class,
		COALESCE(keyword, '') AS keyword,
		COALESCE(label, '')   AS label,
		COALESCE(title, '')   AS title,
		SUM(duration_seconds) AS total_seconds
	FROM sessions
	WHERE duration_seconds >= 10
	GROUP BY app_class, COALESCE(keyword, title)
	ORDER BY app_class, total_seconds DESC
	`

	offset := fmt.Sprintf("-%d days", days-1)
	rows, err := a.db.QueryContext(ctx, q, offset)
	if err != nil {
		return nil, fmt.Errorf("recent by keyword: %w", err)
	}
	defer rows.Close()

	results := []KeywordTotal{}
	for rows.Next() {
		var r KeywordTotal
		if err := rows.Scan(&r.AppClass, &r.Keyword, &r.Label, &r.Title, &r.TotalSeconds); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// DailyAppTotal is one (day, app) row in a time series.
type DailyAppTotal struct {
	Day          string `json:"day"`
	AppClass     string `json:"app_class"`
	TotalSeconds int    `json:"total_seconds"`
	SessionCount int    `json:"session_count"`
}

// History returns per-day per-app totals for the last `days` local days,
// ordered newest day first then by total descending. Powers trend charts.
func (a *Analyzer) History(ctx context.Context, days int) ([]DailyAppTotal, error) {
	const q = `
	WITH
	bounds AS (
		SELECT
			CAST(strftime('%s', date('now','localtime', ?),'utc')       AS INTEGER) AS start_ts,
			CAST(strftime('%s', date('now','localtime','+1 day'),'utc') AS INTEGER) AS end_ts
	),
	gapped AS (
		SELECT
			ts,
			app_class,
			date(ts,'unixepoch','localtime') AS day,
			ts - LAG(ts) OVER (
				PARTITION BY app_class, date(ts,'unixepoch','localtime')
				ORDER BY ts
			) AS gap
		FROM samples, bounds
		WHERE ts >= bounds.start_ts AND ts < bounds.end_ts
	),
	flagged AS (
		SELECT ts, app_class, day,
			CASE WHEN gap IS NULL OR gap > 30 THEN 1 ELSE 0 END AS is_new_session
		FROM gapped
	),
	sessioned AS (
		SELECT ts, app_class, day,
			SUM(is_new_session) OVER (
				PARTITION BY app_class, day ORDER BY ts
				ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW
			) AS session_id
		FROM flagged
	),
	sessions AS (
		SELECT app_class, day, session_id,
			MAX(ts) - MIN(ts) + 5 AS duration_seconds
		FROM sessioned
		GROUP BY app_class, day, session_id
	)
	SELECT
		day,
		app_class,
		SUM(duration_seconds) AS total_seconds,
		COUNT(*)              AS session_count
	FROM sessions
	WHERE duration_seconds >= 10
	GROUP BY day, app_class
	ORDER BY day DESC, total_seconds DESC
	`
	offset := fmt.Sprintf("-%d days", days-1)
	rows, err := a.db.QueryContext(ctx, q, offset)
	if err != nil {
		return nil, fmt.Errorf("history: %w", err)
	}
	defer rows.Close()

	results := []DailyAppTotal{}
	for rows.Next() {
		var r DailyAppTotal
		if err := rows.Scan(&r.Day, &r.AppClass, &r.TotalSeconds, &r.SessionCount); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// TitleTotal is one window-title aggregation for an app.
type TitleTotal struct {
	Title        string `json:"title"`
	TotalSeconds int    `json:"total_seconds"`
	SessionCount int    `json:"session_count"`
}

// DailyTotal is one day in a per-app time series.
type DailyTotal struct {
	Day          string `json:"day"`
	TotalSeconds int    `json:"total_seconds"`
	SessionCount int    `json:"session_count"`
}

// AppDetail bundles everything the UI needs to render a single-app dashboard
// over the last `days` local days.
type AppDetail struct {
	AppClass       string         `json:"app_class"`
	Days           int            `json:"days"`
	TotalSeconds   int            `json:"total_seconds"`
	SessionCount   int            `json:"session_count"`
	LongestSession int            `json:"longest_session_seconds"`
	ByDay          []DailyTotal   `json:"by_day"`
	TopTitles      []TitleTotal   `json:"top_titles"`
	ByKeyword      []KeywordTotal `json:"by_keyword"`
}

// AppDetailFor returns the full per-app drill-down. Composed of several small
// queries instead of one mega-CTE — easier to read and maintain.
func (a *Analyzer) AppDetailFor(ctx context.Context, appClass string, days int) (*AppDetail, error) {
	if days < 1 {
		days = 1
	}
	offset := fmt.Sprintf("-%d days", days-1)

	d := &AppDetail{AppClass: appClass, Days: days, ByDay: []DailyTotal{}, TopTitles: []TitleTotal{}, ByKeyword: []KeywordTotal{}}

	// 1. Totals + longest session.
	const totalsQ = `
	WITH
	bounds AS (
		SELECT
			CAST(strftime('%s', date('now','localtime', ?),'utc')       AS INTEGER) AS start_ts,
			CAST(strftime('%s', date('now','localtime','+1 day'),'utc') AS INTEGER) AS end_ts
	),
	gapped AS (
		SELECT ts,
			ts - LAG(ts) OVER (ORDER BY ts) AS gap
		FROM samples, bounds
		WHERE ts >= bounds.start_ts AND ts < bounds.end_ts
		  AND app_class = ?
	),
	flagged AS (
		SELECT ts, CASE WHEN gap IS NULL OR gap > 30 THEN 1 ELSE 0 END AS is_new_session
		FROM gapped
	),
	sessioned AS (
		SELECT ts,
			SUM(is_new_session) OVER (ORDER BY ts ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) AS session_id
		FROM flagged
	),
	sessions AS (
		SELECT session_id, MAX(ts) - MIN(ts) + 5 AS duration_seconds
		FROM sessioned
		GROUP BY session_id
	)
	SELECT
		COALESCE(SUM(duration_seconds), 0) AS total_seconds,
		COUNT(*)                           AS session_count,
		COALESCE(MAX(duration_seconds), 0) AS longest_session
	FROM sessions
	WHERE duration_seconds >= 10
	`
	if err := a.db.QueryRowContext(ctx, totalsQ, offset, appClass).Scan(
		&d.TotalSeconds, &d.SessionCount, &d.LongestSession,
	); err != nil {
		return nil, fmt.Errorf("app detail totals: %w", err)
	}

	// 2. Daily breakdown.
	const byDayQ = `
	WITH
	bounds AS (
		SELECT
			CAST(strftime('%s', date('now','localtime', ?),'utc')       AS INTEGER) AS start_ts,
			CAST(strftime('%s', date('now','localtime','+1 day'),'utc') AS INTEGER) AS end_ts
	),
	gapped AS (
		SELECT ts, date(ts,'unixepoch','localtime') AS day,
			ts - LAG(ts) OVER (PARTITION BY date(ts,'unixepoch','localtime') ORDER BY ts) AS gap
		FROM samples, bounds
		WHERE ts >= bounds.start_ts AND ts < bounds.end_ts
		  AND app_class = ?
	),
	flagged AS (
		SELECT ts, day, CASE WHEN gap IS NULL OR gap > 30 THEN 1 ELSE 0 END AS is_new_session
		FROM gapped
	),
	sessioned AS (
		SELECT ts, day,
			SUM(is_new_session) OVER (PARTITION BY day ORDER BY ts ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) AS session_id
		FROM flagged
	),
	sessions AS (
		SELECT day, session_id, MAX(ts) - MIN(ts) + 5 AS duration_seconds
		FROM sessioned
		GROUP BY day, session_id
	)
	SELECT day, SUM(duration_seconds) AS total_seconds, COUNT(*) AS session_count
	FROM sessions
	WHERE duration_seconds >= 10
	GROUP BY day
	ORDER BY day DESC
	`
	rows, err := a.db.QueryContext(ctx, byDayQ, offset, appClass)
	if err != nil {
		return nil, fmt.Errorf("app detail by day: %w", err)
	}
	for rows.Next() {
		var r DailyTotal
		if err := rows.Scan(&r.Day, &r.TotalSeconds, &r.SessionCount); err != nil {
			rows.Close()
			return nil, err
		}
		d.ByDay = append(d.ByDay, r)
	}
	rows.Close()

	// 3. Top titles (raw sample count × 5 s; not session-correct but good enough
	// for "what was I doing in this app"). Limit 20 keeps the UI manageable.
	const topTitlesQ = `
	WITH bounds AS (
		SELECT
			CAST(strftime('%s', date('now','localtime', ?),'utc')       AS INTEGER) AS start_ts,
			CAST(strftime('%s', date('now','localtime','+1 day'),'utc') AS INTEGER) AS end_ts
	)
	SELECT
		title,
		COUNT(*) * 5 AS total_seconds,
		1            AS session_count
	FROM samples, bounds
	WHERE ts >= bounds.start_ts AND ts < bounds.end_ts
	  AND app_class = ?
	  AND title IS NOT NULL
	GROUP BY title
	ORDER BY total_seconds DESC
	LIMIT 20
	`
	rows, err = a.db.QueryContext(ctx, topTitlesQ, offset, appClass)
	if err != nil {
		return nil, fmt.Errorf("app detail top titles: %w", err)
	}
	for rows.Next() {
		var r TitleTotal
		if err := rows.Scan(&r.Title, &r.TotalSeconds, &r.SessionCount); err != nil {
			rows.Close()
			return nil, err
		}
		d.TopTitles = append(d.TopTitles, r)
	}
	rows.Close()

	// 4. Keyword breakdown for this app.
	const byKeywordQ = `
	WITH
	bounds AS (
		SELECT
			CAST(strftime('%s', date('now','localtime', ?),'utc')       AS INTEGER) AS start_ts,
			CAST(strftime('%s', date('now','localtime','+1 day'),'utc') AS INTEGER) AS end_ts
	),
	matched AS (
		SELECT s.ts, k.id AS kw_id, k.keyword, COALESCE(k.label, k.keyword) AS label
		FROM samples s, bounds b
		JOIN tracked_keywords k
		  ON k.app_class = s.app_class
		 AND s.title LIKE '%' || k.keyword || '%'
		WHERE s.ts >= b.start_ts AND s.ts < b.end_ts
		  AND s.app_class = ?
		  AND s.title IS NOT NULL
	),
	gapped AS (
		SELECT ts, kw_id, keyword, label,
			ts - LAG(ts) OVER (PARTITION BY kw_id ORDER BY ts) AS gap
		FROM matched
	),
	flagged AS (
		SELECT *, CASE WHEN gap IS NULL OR gap > 30 THEN 1 ELSE 0 END AS is_new_session
		FROM gapped
	),
	sessioned AS (
		SELECT *,
			SUM(is_new_session) OVER (PARTITION BY kw_id ORDER BY ts ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) AS session_id
		FROM flagged
	),
	sessions AS (
		SELECT kw_id, keyword, label, session_id,
			MAX(ts) - MIN(ts) + 5 AS duration_seconds
		FROM sessioned
		GROUP BY kw_id, session_id
	)
	SELECT keyword, label,
		SUM(duration_seconds) AS total_seconds,
		COUNT(*)              AS session_count
	FROM sessions
	WHERE duration_seconds >= 10
	GROUP BY kw_id
	ORDER BY total_seconds DESC
	`
	rows, err = a.db.QueryContext(ctx, byKeywordQ, offset, appClass)
	if err != nil {
		return nil, fmt.Errorf("app detail by keyword: %w", err)
	}
	for rows.Next() {
		r := KeywordTotal{AppClass: appClass}
		if err := rows.Scan(&r.Keyword, &r.Label, &r.TotalSeconds, &r.SessionCount); err != nil {
			rows.Close()
			return nil, err
		}
		d.ByKeyword = append(d.ByKeyword, r)
	}
	rows.Close()

	return d, nil
}

// KeywordTotals returns the tracked-keyword list enriched with session totals
// over the last `days` local days. Keywords with no matches return zero — the
// UI can show them as "tracked but inactive".
func (a *Analyzer) KeywordTotals(ctx context.Context, days int) ([]KeywordTotal, error) {
	if days < 1 {
		days = 1
	}
	offset := fmt.Sprintf("-%d days", days-1)

	const q = `
	WITH
	bounds AS (
		SELECT
			CAST(strftime('%s', date('now','localtime', ?),'utc')       AS INTEGER) AS start_ts,
			CAST(strftime('%s', date('now','localtime','+1 day'),'utc') AS INTEGER) AS end_ts
	),
	matched AS (
		SELECT s.ts, k.id AS kw_id
		FROM samples s, bounds b
		JOIN tracked_keywords k
		  ON k.app_class = s.app_class
		 AND s.title LIKE '%' || k.keyword || '%'
		WHERE s.ts >= b.start_ts AND s.ts < b.end_ts
		  AND s.title IS NOT NULL
	),
	gapped AS (
		SELECT ts, kw_id,
			ts - LAG(ts) OVER (PARTITION BY kw_id ORDER BY ts) AS gap
		FROM matched
	),
	flagged AS (
		SELECT ts, kw_id, CASE WHEN gap IS NULL OR gap > 30 THEN 1 ELSE 0 END AS is_new_session
		FROM gapped
	),
	sessioned AS (
		SELECT ts, kw_id,
			SUM(is_new_session) OVER (PARTITION BY kw_id ORDER BY ts ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) AS session_id
		FROM flagged
	),
	sessions AS (
		SELECT kw_id, session_id, MAX(ts) - MIN(ts) + 5 AS duration_seconds
		FROM sessioned
		GROUP BY kw_id, session_id
	),
	totals AS (
		SELECT kw_id,
			SUM(duration_seconds) AS total_seconds,
			COUNT(*)              AS session_count
		FROM sessions
		WHERE duration_seconds >= 10
		GROUP BY kw_id
	)
	SELECT
		k.app_class,
		k.keyword,
		COALESCE(k.label, k.keyword)       AS label,
		COALESCE(t.total_seconds, 0)       AS total_seconds,
		COALESCE(t.session_count, 0)       AS session_count
	FROM tracked_keywords k
	LEFT JOIN totals t ON t.kw_id = k.id
	ORDER BY total_seconds DESC, k.app_class, k.keyword
	`
	rows, err := a.db.QueryContext(ctx, q, offset)
	if err != nil {
		return nil, fmt.Errorf("keyword totals: %w", err)
	}
	defer rows.Close()

	results := []KeywordTotal{}
	for rows.Next() {
		var r KeywordTotal
		if err := rows.Scan(&r.AppClass, &r.Keyword, &r.Label, &r.TotalSeconds, &r.SessionCount); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

