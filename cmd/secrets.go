package cmd

import (
	"fmt"
	"os"

	"github.com/samur005/3s-ui/config"
	"github.com/samur005/3s-ui/database"
)

// secretsDecrypt writes node tokens and mTLS keys back as plaintext so an
// older release (no encryption at rest) can read them after a downgrade.
// The next start of this release seals them again.
func secretsDecrypt() {
	if err := database.InitDB(config.GetDBPath()); err != nil {
		fmt.Println("secrets: open database failed:", err)
		os.Exit(1)
	}
	n, err := database.UnsealNodeSecrets()
	if err != nil {
		fmt.Println("secrets: decrypt failed:", err)
		os.Exit(1)
	}
	fmt.Printf("secrets: %d node row(s) written back as plaintext; stop the panel and start the older release now\n", n)
}
