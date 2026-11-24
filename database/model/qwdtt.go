package model

// QwdttState is the single-row table behind the qWDTT page (Id is always 1).
//
// It is a table of its own rather than a few settings rows: the Settings page
// loads every settings row into the browser and saves them back as a whole, so
// VK session cookies and the shared password would travel on every settings
// save, and a stale tab could overwrite them.
type QwdttState struct {
	Id uint `json:"id" gorm:"primaryKey"`
	// Config is the JSON of qwdtt.Config.
	Config string `json:"-"`
	// VkCookies is the cookie JSON the VK call API is driven with: an array of
	// {"name","value"} objects, as exported from a logged-in browser session.
	VkCookies string `json:"-"`
	// VkCalls is the JSON list of VK calls the panel created itself, so the
	// matching hash can be told apart from one pasted in by hand.
	VkCalls string `json:"-"`
	// Wdtt is the JSON of the optional remote WDTT panel the hashes can also be
	// created through (qwdtt.RemoteSource: url, token or username/password),
	// the LucX-compatible source for a panel without VK cookies of its own.
	Wdtt string `json:"-"`
}

// QwdttUsage is the last byte counters read from the qWDTT server's
// passwords.json for one per-client password. The server only ever adds to
// them, so the difference to the previous read is the traffic to credit to
// the client; a counter that went down means the entry was recreated and its
// whole value is new.
type QwdttUsage struct {
	Password   string `json:"-" gorm:"primaryKey;size:64"`
	ClientName string `json:"clientName" gorm:"index;size:255"`
	Up         int64  `json:"up" gorm:"default:0;not null"`
	Down       int64  `json:"down" gorm:"default:0;not null"`
}
