package auth

import "errors"

// ErrConfigPersistence 表示配置已在内存变更，但写入配置文件失败（未能持久化）。
var ErrConfigPersistence = errors.New("配置文件持久化失败")
