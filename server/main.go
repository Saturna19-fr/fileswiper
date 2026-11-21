// FileSwiper server: exposes a folder to the iOS app, which decides file by
// file what is kept (moved to « Validé », tree preserved) or deleted.
package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
)

func main() {
	cfg, dataDir, err := loadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Erreur :", err)
		os.Exit(1)
	}
	for _, d := range []string{cfg.Validated, cfg.Trash} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			fmt.Fprintln(os.Stderr, "Erreur :", err)
			os.Exit(1)
		}
	}

	store := NewStore(cfg, dataDir)
	go func() {
		store.Rescan()
		st := store.Status()
		log.Printf("%d fichiers à trier, %d groupes de doublons", st.Pending, st.DuplicateGroups)
	}()

	fmt.Println("FileSwiper")
	fmt.Println("  À trier   :", cfg.Source)
	fmt.Println("  Validé    :", cfg.Validated)
	fmt.Println("  Supprimé  :", cfg.Trash)
	fmt.Println("  Code      :", cfg.Token)
	for _, ip := range localIPs() {
		fmt.Printf("  Adresse   : %s:%d\n", ip, cfg.Port)
	}

	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", cfg.Port), newAPI(store)))
}

func localIPs() []string {
	var out []string
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && !n.IP.IsLoopback() && n.IP.To4() != nil {
			out = append(out, n.IP.String())
		}
	}
	return out
}
