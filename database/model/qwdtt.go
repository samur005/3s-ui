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
}
