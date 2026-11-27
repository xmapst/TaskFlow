package iproto

type Replay struct {
	Date string `json:"date"`
	ID   string `json:"id"`
	Size int64  `json:"size"` // compressed bytes
}

type ReplayList struct {
	Items   []Replay `json:"items"`
	Total   int64    `json:"total"`
	HasMore bool     `json:"hasMore"`
}
