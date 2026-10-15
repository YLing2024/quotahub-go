// Package settings 提供运行时设置的业务逻辑（JSON 文件持久化）。
//
// 语义对齐旧实现 services/settingsService.ts：`collectIntervalSeconds` 为数字且 >= 0 时
// 取 floor 并钳制到 86400；否则回退默认值（0 = 关闭自动采集）。
package settings

import (
	"math"
	"path/filepath"
	"sync"

	"quotahub-go/internal/jsonstore"
	"quotahub-go/internal/jsval"
	"quotahub-go/internal/model"
)

// Default 是默认设置（自动采集关闭）。
var Default = model.Settings{CollectIntervalSeconds: 0}

// Service 是设置的数据访问 + 业务逻辑。
type Service struct {
	file string
	mu   sync.Mutex
}

// New 构造设置服务；dataDir 下使用 settings.json（与旧实现同名）。
func New(dataDir string) *Service {
	return &Service{file: filepath.Join(dataDir, "settings.json")}
}

// Get 读取当前设置（缺失 / 损坏回默认值）。
func (s *Service) Get() model.Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getLocked()
}

// Update 校验并合并设置补丁，返回更新后的设置。
func (s *Service) Update(patch any) model.Settings {
	p := jsval.ObjOrEmpty(patch)
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.getLocked()
	if v, ok := p["collectIntervalSeconds"]; ok {
		if n, valid := jsval.Number(v); valid && jsval.Finite(n) && n >= 0 {
			current.CollectIntervalSeconds = int(math.Min(math.Floor(n), 86400))
		} else {
			current.CollectIntervalSeconds = Default.CollectIntervalSeconds
		}
	}
	_ = jsonstore.WriteAtomic(s.file, current)
	return current
}

func (s *Service) getLocked() model.Settings {
	raw := jsonstore.Read[map[string]any](s.file, nil)
	out := Default
	if raw == nil {
		return out
	}
	if v, ok := raw["collectIntervalSeconds"]; ok {
		if n, valid := jsval.Number(v); valid && jsval.Finite(n) && n >= 0 {
			out.CollectIntervalSeconds = int(math.Floor(n))
		}
	}
	return out
}
