package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/adminops"
	"github.com/AprendendoLinux/corujabackup/internal/config"
	"github.com/AprendendoLinux/corujabackup/internal/db"
	"github.com/AprendendoLinux/corujabackup/internal/store"
)

const adminHelp = `Uso:
  coruja-server admin reset-password -user NOME [-disable-2fa]   define nova senha (e opcionalmente desativa o 2FA)
  coruja-server admin create-admin   -user NOME                  cria um administrador

A nova senha é lida da variável CORUJA_NEW_PASSWORD ou, se ela não existir, da entrada padrão (uma linha).
Exemplo (dentro do contêiner):  docker exec -it corujabackup-server coruja-server admin reset-password -user henrique -disable-2fa
`

func adminCmd(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, adminHelp)
		return 2
	}
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	user := fs.String("user", "", "nome do usuário")
	dis2fa := fs.Bool("disable-2fa", false, "desativa o 2FA do usuário")
	if err := fs.Parse(args[1:]); err != nil || *user == "" {
		fmt.Fprint(os.Stderr, adminHelp)
		return 2
	}
	cfg, err := config.Load(config.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "configuração inválida:", err)
		return 2
	}
	pass := os.Getenv("CORUJA_NEW_PASSWORD")
	if pass == "" {
		fmt.Fprint(os.Stderr, "Nova senha (mínimo 10 caracteres): ")
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		pass = strings.TrimRight(line, "\r\n")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	d, err := db.Open(ctx, cfg.DSN(), 30*time.Second)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sem banco de dados:", err)
		return 1
	}
	defer d.Close()
	st := store.New(d)
	switch args[0] {
	case "reset-password":
		err = adminops.ResetPassword(ctx, st, *user, pass, *dis2fa)
	case "create-admin":
		err = adminops.CreateAdmin(ctx, st, *user, pass)
	default:
		fmt.Fprint(os.Stderr, adminHelp)
		return 2
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "erro:", err)
		return 1
	}
	fmt.Println("ok: operação concluída para o usuário", strings.ToLower(*user))
	return 0
}
