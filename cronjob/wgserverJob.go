package cronjob

import "github.com/samur005/3s-ui/wgserver"

// WgserverJob keeps the WireGuard / AmneziaWG devices, their peers and their
// routing in line with the stored inbounds and clients.
type WgserverJob struct{}

func NewWgserverJob() *WgserverJob { return &WgserverJob{} }

func (j *WgserverJob) Run() { wgserver.Get().Reconcile() }
