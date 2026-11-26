package cloud

// Aplicativo OAuth padrão do Coruja Backup para o Google Drive: o usuário só autoriza a própria conta (código no navegador),
// sem registrar aplicativo nem colar credenciais. O ID do cliente é público. O segredo NÃO fica no repositório: é injetado na
// compilação das imagens oficiais (-ldflags -X) e é o "segredo" de um aplicativo do tipo TVs/dispositivos, que o Google
// documenta como não confidencial. Em compilações sem o segredo (desenvolvimento, forks) o modo padrão fica desligado.
var (
	DefaultGoogleClientID     = "808370832157-1j2fn12fpednq018lqbiuvnd7c1v37v0.apps.googleusercontent.com"
	DefaultGoogleClientSecret = "" // -X github.com/AprendendoLinux/corujabackup/internal/cloud.DefaultGoogleClientSecret=...
)

// HasDefaultApp informa se há aplicativo padrão compilado para o tipo de nuvem.
func HasDefaultApp(typ string) bool {
	return typ == TypeGDrive && DefaultGoogleClientID != "" && DefaultGoogleClientSecret != ""
}
