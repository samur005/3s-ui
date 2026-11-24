package sub

// The qWDTT side of the subscription server.
//
// qwdtt:// links ride in the plain link list next to every other protocol
// (withQwdtt), unless the operator turned that off (qwdttSubLinks): proxy
// apps that do not know the scheme skip the line, but a strict one may
// reject the whole list.
//
// The qWDTT apps themselves do not read such a list. The Android app (and
// qWDTT-linux) import a subscription URL that answers with either a single
// qwdtt://config? link or a JSON document {subscriptionName, profiles:[...]}
// (SpaceNeuroX/proxy-turn-vk-android SubscriptionImport.kt). ?format=qwdtt
// answers with that document, one profile per qwdtt link of the client --
// this panel's and every node's -- and the app's own User-Agent
// ("qWDTT-Subscription/1.0") gets it on the bare URL too.

// qwdttInLinks reports whether qwdtt:// links go into the plain list.
func (s *SubService) qwdttInLinks() bool {
	v, err := s.SettingService.GetQwdttSubLinks()
	return err != nil || v
}

// getQwdttSub is the ?format=qwdtt answer.
func (s *SubService) getQwdttSub(subId string) (*string, []string, error) {
	client, err := s.getClientBySubId(subId)
	if err != nil {
		return nil, nil, err
	}
	links := make([]string, 0, 2)
	for _, l := range s.qwdtt.ClientLinks(client) {
		links = append(links, l.Link)
	}
	doc, err := s.qwdtt.ClientSubscription(client, links)
	if err != nil {
		return nil, nil, err
	}
	return &doc, s.getClientHeaders(client), nil
}
