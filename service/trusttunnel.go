package service

import (
	"encoding/json"

	"github.com/samur005/3s-ui/util/common"
	"gorm.io/gorm"
)

func init() {
	registerExtDriver(&extDriver{
		Type:   "trusttunnel",
		Engine: EngineExternal,
		Normalize: func(tx *gorm.DB, id uint, options, stored json.RawMessage) (json.RawMessage, error) {
			var opt struct {
				ListenPort  int    `json:"listen_port"`
				Credentials string `json:"credentials"`
			}
			if err := json.Unmarshal(options, &opt); err != nil {
				return nil, err
			}
			if opt.ListenPort <= 0 || opt.ListenPort > 65535 {
				return nil, common.NewError("invalid listen_port")
			}
			return json.Marshal(opt)
		},
		Apply: func() {
			// TODO: start/stop trusttunnel_endpoint process based on DB state
		},
	})
}
