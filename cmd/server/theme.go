package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/AprendendoLinux/corujabackup/internal/themepkg"
)

const themeHelp = `Uso:
  coruja-server theme validate ARQUIVO.zip        valida um pacote de tema (não precisa de banco de dados)
  coruja-server theme pack PASTA SAIDA.zip        valida a pasta e gera o pacote .zip
Documentação do formato: docs/TEMAS.md
`

func themeCmd(args []string) int {
	switch {
	case len(args) == 2 && args[0] == "validate":
		b, err := os.ReadFile(args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, "erro:", err)
			return 1
		}
		p, err := themepkg.Read(b)
		if err != nil {
			fmt.Fprintln(os.Stderr, "✖ pacote inválido:", err)
			return 1
		}
		fmt.Printf("✔ pacote válido: %s (%s) versão %s\n", p.Name, p.ID, p.Version)
		return 0
	case len(args) == 3 && args[0] == "pack":
		b, err := themepkg.Pack(args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, "✖", err)
			return 1
		}
		if err := os.WriteFile(args[2], b, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "erro:", err)
			return 1
		}
		fmt.Printf("✔ %s gerado (%d bytes)\n", filepath.Base(args[2]), len(b))
		return 0
	}
	fmt.Fprint(os.Stderr, themeHelp)
	return 2
}
