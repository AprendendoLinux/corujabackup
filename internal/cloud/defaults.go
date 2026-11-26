package cloud

import "strings"

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

// Aplicativo "Aplicativo da Web" do Google (login por popup, com retorno em OAuthBrokerURL). O ID é público; o segredo é
// injetado na compilação (-X ...cloud.DefaultGoogleWebClientSecret=...) e nunca fica no repositório.
var (
	DefaultGoogleWebClientID     = "808370832157-vjk5g9bs0b0a3gntcvfsfo0ktsu9km7v.apps.googleusercontent.com"
	DefaultGoogleWebClientSecret = ""
	// OAuthBrokerURL é a página pública que recebe a volta do provedor e entrega o código à janela do Coruja (veja site/).
	OAuthBrokerURL = "https://coruja.henrique.tec.br"
)

// Aplicativo da Microsoft (cliente público, sem segredo) para o login por popup do OneDrive. O ID é público.
var DefaultMicrosoftClientID = "ec0121f5-4ea0-470a-860d-705b6dea180e"

// DefaultDropboxAppKey é a chave (pública) do aplicativo do Dropbox: login por popup com PKCE, sem segredo.
var DefaultDropboxAppKey = "29ptnyvv0n28wtf"

// HasWebApp informa se o login por popup (aplicativo da Web) está disponível para o tipo de nuvem.
func HasWebApp(typ string) bool {
	if OAuthBrokerURL == "" {
		return false
	}
	switch typ {
	case TypeGDrive:
		return DefaultGoogleWebClientID != "" && DefaultGoogleWebClientSecret != ""
	case TypeOneDrive:
		return DefaultMicrosoftClientID != ""
	case TypeDropbox:
		return DefaultDropboxAppKey != ""
	}
	return false
}

// WebAppCreds devolve o ID (e o segredo, se houver) do aplicativo de login por popup do tipo de nuvem.
func WebAppCreds(typ string) (id, secret string) {
	switch typ {
	case TypeGDrive:
		return DefaultGoogleWebClientID, DefaultGoogleWebClientSecret
	case TypeOneDrive:
		return DefaultMicrosoftClientID, ""
	case TypeDropbox:
		return DefaultDropboxAppKey, ""
	}
	return "", ""
}

// BrokerRedirect devolve o endereço de retorno cadastrado no provedor para o tipo de nuvem.
func BrokerRedirect(typ string) string {
	switch typ {
	case TypeGDrive:
		return strings.TrimRight(OAuthBrokerURL, "/") + "/oauth/google/callback"
	case TypeOneDrive:
		return strings.TrimRight(OAuthBrokerURL, "/") + "/oauth/microsoft/callback"
	case TypeDropbox:
		return strings.TrimRight(OAuthBrokerURL, "/") + "/oauth/dropbox/callback"
	}
	return ""
}
