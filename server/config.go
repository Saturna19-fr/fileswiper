package main

import (
	"bufio"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Source    string `json:"source"`
	Validated string `json:"validated"`
	Trash     string `json:"trash"`
	Port      int    `json:"port"`
	Token     string `json:"token"`
}

// loadConfig merges fileswiper.json and command-line flags, fills defaults,
// and writes the result back so the next launch needs no arguments.
func loadConfig() (Config, string, error) {
	cfgPath := flag.String("config", "fileswiper.json", "fichier de configuration")
	source := flag.String("source", "", "dossier à trier")
	validated := flag.String("validated", "", "dossier des fichiers gardés (défaut : « Validé » à côté de la source)")
	trash := flag.String("trash", "", "dossier des fichiers supprimés (défaut : « Supprimé » à côté de la source)")
	port := flag.Int("port", 0, "port HTTP (défaut : 8787)")
	token := flag.String("token", "", "code d'accès (généré si absent)")
	flag.Parse()

	var cfg Config
	if b, err := os.ReadFile(*cfgPath); err == nil {
		if err := json.Unmarshal(b, &cfg); err != nil {
			return cfg, "", fmt.Errorf("%s : %w", *cfgPath, err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return cfg, "", err
	}
	if *source != "" {
		cfg.Source = *source
	}
	if *validated != "" {
		cfg.Validated = *validated
	}
	if *trash != "" {
		cfg.Trash = *trash
	}
	if *port != 0 {
		cfg.Port = *port
	}
	if *token != "" {
		cfg.Token = *token
	}

	if cfg.Source == "" {
		fmt.Print("Dossier à trier : ")
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		cfg.Source = strings.Trim(strings.TrimSpace(line), `"`)
		if cfg.Source == "" {
			return cfg, "", errors.New("aucun dossier source")
		}
	}
	if err := cfg.normalize(); err != nil {
		return cfg, "", err
	}

	b, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(*cfgPath, b, 0o600); err != nil {
		return cfg, "", err
	}
	abs, _ := filepath.Abs(*cfgPath)
	return cfg, filepath.Dir(abs), nil
}

func (c *Config) normalize() error {
	var err error
	if c.Source, err = filepath.Abs(c.Source); err != nil {
		return err
	}
	if st, err := os.Stat(c.Source); err != nil || !st.IsDir() {
		return fmt.Errorf("dossier source introuvable : %s", c.Source)
	}
	if c.Validated == "" {
		c.Validated = filepath.Join(filepath.Dir(c.Source), "Validé")
	}
	if c.Trash == "" {
		c.Trash = filepath.Join(filepath.Dir(c.Source), "Supprimé")
	}
	if c.Validated, err = filepath.Abs(c.Validated); err != nil {
		return err
	}
	if c.Trash, err = filepath.Abs(c.Trash); err != nil {
		return err
	}
	for _, d := range []string{c.Validated, c.Trash} {
		if samePath(d, c.Source) || isInside(c.Source, d) {
			return fmt.Errorf("la source ne peut pas être dans %s", d)
		}
	}
	if samePath(c.Validated, c.Trash) {
		return errors.New("Validé et Supprimé doivent être deux dossiers distincts")
	}
	if c.Port == 0 {
		c.Port = 8787
	}
	if c.Token == "" {
		c.Token = randomToken(8)
	}
	return nil
}

func randomToken(n int) string {
	const alphabet = "abcdefghjkmnpqrstuvwxyz23456789" // no look-alikes: typed on a phone
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

func samePath(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// isInside reports whether path is strictly below dir.
func isInside(path, dir string) bool {
	p := strings.ToLower(filepath.Clean(path))
	d := strings.ToLower(filepath.Clean(dir))
	if !strings.HasSuffix(d, string(filepath.Separator)) {
		d += string(filepath.Separator)
	}
	return strings.HasPrefix(p, d)
}
