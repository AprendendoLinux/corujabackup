// Package version expõe a versão do Coruja Backup (injetada no build via -ldflags).
package version

// Version é sobrescrita no build: -ldflags "-X github.com/AprendendoLinux/corujabackup/internal/version.Version=1.2.3".
var Version = "0.0.16"

const Name = "Coruja Backup"
