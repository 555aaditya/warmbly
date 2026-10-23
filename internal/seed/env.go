package seed

import (
	"fmt"
	"os"
	"strings"
)

// DevEnvironment refuses unless APP_ENV names local development explicitly.
// The seeders plant accounts with published credentials, so an unset APP_ENV
// refuses too.
func DevEnvironment() error {
	switch env := strings.ToLower(strings.TrimSpace(os.Getenv("APP_ENV"))); env {
	case "dev", "development", "local":
		return nil
	case "":
		return fmt.Errorf("APP_ENV is not set. This seeder creates accounts with published credentials and runs only with APP_ENV=dev")
	default:
		return fmt.Errorf("APP_ENV=%s. This seeder creates accounts with published credentials and runs only with APP_ENV=dev", env)
	}
}
