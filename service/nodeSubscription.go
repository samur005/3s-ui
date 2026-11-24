package service

import (
	"encoding/json"
	"strings"

	"github.com/samur005/3s-ui/database"
	"github.com/samur005/3s-ui/database/model"
)

// Subscription aggregation, node side of it.
//
// A client's subscription already carries every bound node's servers: the
// reconcile (refreshNodeLinks) regenerates them from the master's replica rows
// and stores them in client.Links as "external" entries under a "[node] "
// remark prefix. That is what keeps the subscription working when a node is
// down -- the master serves the last good links instead of asking a node that
// does not answer -- and why nothing is fetched from nodes at request time.
//
// What serving still needs is the policy between "temporarily unreachable" and
// "gone":
//
//   - offline node:            links kept and served (stored copy)
//   - node with SubExclude:    links kept in the DB, left out of the answer
//   - disabled node:           unchanged from before -- links served, nothing
//                              synced; set SubExclude as well to hide them
//   - deleted node:            refused while it has replicas; the replicas'
//                              removal strips their links
//
// FilterNodeLinks applies it to one client's stored links, in memory only, and
// drops exact duplicate external URIs (the same server bound twice through two
// replicas must not appear twice in the client app).

// excludedNodePrefixes returns the "[name] " prefixes of nodes whose servers
// are currently left out of subscriptions.
func excludedNodePrefixes() []string {
	var names []string
	db := database.GetDB()
	if db == nil {
		return nil
	}
	if err := db.Model(model.Node{}).Where("sub_exclude = ?", true).Pluck("name", &names).Error; err != nil {
		return nil
	}
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, nodeLinkPrefix(n))
	}
	return out
}

// FilterNodeLinks returns links without the external entries of excluded
// nodes and without duplicate external URIs. Malformed JSON is returned as-is:
// the link services already tolerate it, and a filter must not make a broken
// row worse.
func FilterNodeLinks(links json.RawMessage) json.RawMessage {
	return filterNodeLinks(links, excludedNodePrefixes())
}

func filterNodeLinks(links json.RawMessage, excluded []string) json.RawMessage {
	if len(links) == 0 {
		return links
	}
	var entries []map[string]interface{}
	if err := json.Unmarshal(links, &entries); err != nil {
		return links
	}
	kept := make([]map[string]interface{}, 0, len(entries))
	seen := map[string]bool{}
	changed := false
	for _, l := range entries {
		typ, _ := l["type"].(string)
		remark, _ := l["remark"].(string)
		uri, _ := l["uri"].(string)
		if typ == "external" {
			drop := false
			for _, p := range excluded {
				if strings.HasPrefix(remark, p) {
					drop = true
					break
				}
			}
			if !drop && uri != "" {
				if seen[uri] {
					drop = true
				}
				seen[uri] = true
			}
			if drop {
				changed = true
				continue
			}
		}
		kept = append(kept, l)
	}
	if !changed {
		return links
	}
	out, err := json.Marshal(kept)
	if err != nil {
		return links
	}
	return out
}
