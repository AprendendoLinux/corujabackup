package cloud_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/cloud"
	"github.com/AprendendoLinux/corujabackup/internal/cloud/cloudtest"
)

func checkFolders(t *testing.T, p cloud.Provider) {
	t.Helper()
	for parent, want := range map[string][]string{
		"":                     {"Coruja Backup", "Outra"},
		"Coruja Backup":        {"srv1", "srv2"},
		"Coruja Backup/srv1":   {"etc"},
		"Coruja Backup/srv2":   nil,
		"Pasta/que não existe": nil,
	} {
		got, err := p.Folders(context.Background(), parent)
		slices.Sort(got)
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("%T Folders(%q) = %v %v, esperado %v", p, parent, got, err, want)
		}
	}
}

var folderSeed = []string{"Coruja Backup/srv1/etc", "Coruja Backup/srv2", "Outra"}

func TestListagemDePastasNasNuvens(t *testing.T) {
	t.Run("gdrive", func(t *testing.T) {
		fake := cloudtest.NewDrive("gid")
		defer fake.Close()
		for _, f := range folderSeed {
			fake.Put(f, "a.tar.gz", []byte("x"))
		}
		checkFolders(t, gdrive(t, fake))
	})
	t.Run("onedrive", func(t *testing.T) {
		fake := cloudtest.NewGraph("cid")
		defer fake.Close()
		for _, f := range folderSeed {
			fake.Put(f, "a.tar.gz", []byte("x"))
		}
		od, _ := oneDrive(t, fake)
		checkFolders(t, od)
	})
}

type fixedToken string

func (f fixedToken) Access(context.Context) (string, error) { return string(f), nil }

func TestListagemDePastasNoDropbox(t *testing.T) {
	fake := cloudtest.NewDropbox("k")
	defer fake.Close()
	for _, f := range folderSeed {
		fake.Files["/"+f+"/a.tar.gz"] = []byte("x")
	}
	o := cloud.DropboxOAuth("k", "")
	o.TokenURL = fake.URL + "/oauth2/token"
	tok, err := o.RefreshScoped(context.Background(), "dbx-refresh", "", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	checkFolders(t, &cloud.Dropbox{TS: fixedToken(tok.Access), API: fake.URL + "/2"})
}
