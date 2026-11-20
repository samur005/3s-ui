package cronjob

import (
	"github.com/samur005/3s-ui/logger"
	"github.com/samur005/3s-ui/qwdtt"
)

// QwdttJob keeps the qWDTT server and its routing in line with the stored
// configuration: it starts the server, restarts it after a crash, and
// re-installs the routing after a core restart (which recreates the TUN).
type QwdttJob struct{}

func NewQwdttJob() *QwdttJob { return &QwdttJob{} }

func (j *QwdttJob) Run() { qwdtt.Get().Reconcile() }

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
