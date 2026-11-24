package cronjob

import (
	"sync"

	"github.com/samur005/3s-ui/logger"
	"github.com/samur005/3s-ui/qwdtt"
	"github.com/samur005/3s-ui/service"
)

// QwdttJob keeps the qWDTT server and its routing in line with the stored
// configuration: it starts the server, restarts it after a crash, and
// re-installs the routing after a core restart (which recreates the TUN).
type QwdttJob struct{}

func NewQwdttJob() *QwdttJob { return &QwdttJob{} }

func (j *QwdttJob) Run() {
	service.QwdttEnsurePasswords()
	qwdtt.Get().Reconcile()
}

// QwdttNodeJob keeps the replicas of the nodes' qWDTT inbounds current, so the
// node links in subscriptions carry the node's live VK hashes.
type QwdttNodeJob struct {
	service.NodeSyncService
	running sync.Mutex
}

func NewQwdttNodeJob() *QwdttNodeJob { return &QwdttNodeJob{} }

func (j *QwdttNodeJob) Run() {
	if !j.running.TryLock() {
		return
	}
	defer j.running.Unlock()
	j.NodeSyncService.RefreshQwdttReplicas()
}

// QwdttHashJob checks the VK calls behind the configured hashes and replaces
// ended ones. A no-op unless qWDTT and automatic hashes are on and VK cookies
// are stored.
type QwdttHashJob struct{}

func NewQwdttHashJob() *QwdttHashJob { return &QwdttHashJob{} }

func (j *QwdttHashJob) Run() {
	if _, err := qwdtt.MaintainHashes(); err != nil {
		logger.Warning("qwdtt: VK hash maintenance: ", err)
	}
}
