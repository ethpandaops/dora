package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/spf13/cobra"

	"github.com/ethpandaops/dora/types"
	"github.com/ethpandaops/dora/utils"
)

var tokenCmd = &cobra.Command{
	Use:   "token",
	Short: "Generate JWT tokens for API authentication",
	Long:  "Generate JWT tokens for API authentication with configurable rate limits and expiration times",
}

var generateTokenCmd = &cobra.Command{
	Use:   "generate",
	Short: "Generate a new API token",
	Long:  "Generate a new JWT token for API authentication",
	RunE: func(cmd *cobra.Command, args []string) error {
		return generateToken(cmd)
	},
}

var generateSecretCmd = &cobra.Command{
	Use:   "secret",
	Short: "Generate a random secret for token signing",
	Long:  "Generate a cryptographically secure random secret for JWT token signing",
	Run: func(cmd *cobra.Command, args []string) {
		generateSecret()
	},
}

func init() {
	// Add token command to root
	rootCmd.AddCommand(tokenCmd)

	// Add subcommands
	tokenCmd.AddCommand(generateTokenCmd)
	tokenCmd.AddCommand(generateSecretCmd)

	// Token generation flags
	generateTokenCmd.Flags().StringP("name", "n", "", "Token name/identifier (required)")
	generateTokenCmd.Flags().UintP("rate-limit", "r", 0, "Rate limit per minute (0 = unlimited)")
	generateTokenCmd.Flags().StringP("duration", "d", "", "Token duration (e.g. '24h', '7d', '30d', empty = no expiration)")
	generateTokenCmd.Flags().StringP("secret", "s", "", "JWT signing secret (uses config value if not provided)")
	generateTokenCmd.Flags().StringP("config", "", "", "Path to dora config file to load secret from")
	generateTokenCmd.Flags().StringSliceP("cors-origins", "c", []string{}, "Allowed CORS origins (e.g. 'https://example.com,https://*.example.com')")
	generateTokenCmd.Flags().StringSliceP("domain-patterns", "p", []string{}, "Dora instance domain patterns (e.g. 'api.example.com,*.example.com', empty = any domain)")

	// Mark name as required
	generateTokenCmd.MarkFlagRequired("name")
}

func generateToken(cmd *cobra.Command) error {
	// Get flags
	name, _ := cmd.Flags().GetString("name")
	rateLimit, _ := cmd.Flags().GetUint("rate-limit")
	duration, _ := cmd.Flags().GetString("duration")
	secret, _ := cmd.Flags().GetString("secret")
	configPath, _ := cmd.Flags().GetString("config")
	corsOrigins, _ := cmd.Flags().GetStringSlice("cors-origins")
	domainPatterns, _ := cmd.Flags().GetStringSlice("domain-patterns")

	// Load config if path provided
	if configPath != "" {
		cfg := &types.Config{}
		err := utils.ReadConfig(cfg, configPath)
		if err != nil {
			return fmt.Errorf("error reading config file: %v", err)
		}
		utils.Config = cfg
	}

	// Use config secret if not provided
	if secret == "" {
		if utils.Config != nil && utils.Config.Api.AuthSecret != "" {
			secret = utils.Config.Api.AuthSecret
		} else {
			return fmt.Errorf("no JWT secret provided. Use --secret flag, --config flag, or set API_AUTH_SECRET in config")
		}
	}

	// Create token claims
	now := time.Now()
	claims := &types.APITokenClaims{
		Name:           name,
		RateLimit:      rateLimit,
		CorsOrigins:    corsOrigins,
		DomainPatterns: domainPatterns,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt: jwt.NewNumericDate(now),
			Subject:  "api-access",
		},
	}

	// Set expiration if duration provided
	if duration != "" {
		parsedDuration, err := parseDurationWithDays(duration)
		if err != nil {
			return fmt.Errorf("invalid duration format: %v (use format like '24h', '7d', '30d')", err)
		}
		claims.ExpiresAt = jwt.NewNumericDate(now.Add(parsedDuration))
	}

	// Create and sign token
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(secret))
	if err != nil {
		return fmt.Errorf("failed to sign token: %v", err)
	}

	// Print token information
	fmt.Printf("Generated API Token:\n")
	fmt.Printf("==================\n")
	fmt.Printf("Name: %s\n", name)
	fmt.Printf("Rate Limit: ")
	if rateLimit == 0 {
		fmt.Printf("Unlimited\n")
	} else {
		fmt.Printf("%d requests/minute\n", rateLimit)
	}
	fmt.Printf("CORS Origins: ")
	if len(corsOrigins) == 0 {
		fmt.Printf("Uses global config\n")
	} else {
		fmt.Printf("%v\n", corsOrigins)
	}
	fmt.Printf("Dora Instance Domain Patterns: ")
	if len(domainPatterns) == 0 {
		fmt.Printf("Any dora instance allowed\n")
	} else {
		fmt.Printf("%v\n", domainPatterns)
	}
	fmt.Printf("Issued At: %s\n", now.Format(time.RFC3339))
	if claims.ExpiresAt != nil {
		fmt.Printf("Expires At: %s\n", claims.ExpiresAt.Format(time.RFC3339))
	} else {
		fmt.Printf("Expires At: Never\n")
	}
	fmt.Printf("\nToken:\n%s\n", tokenString)
	fmt.Printf("\nUsage:\n")
	fmt.Printf("curl -H \"Authorization: Bearer %s\" http://localhost:8080/api/v1/epochs\n", tokenString)

	return nil
}

func generateSecret() {
	// Generate 32 random bytes
	secretBytes := make([]byte, 32)
	_, err := rand.Read(secretBytes)
	if err != nil {
		fmt.Printf("Error generating secret: %v\n", err)
		return
	}

	// Encode as base64
	secret := base64.StdEncoding.EncodeToString(secretBytes)

	fmt.Printf("Generated JWT Secret:\n")
	fmt.Printf("====================\n")
	fmt.Printf("Secret: %s\n", secret)
	fmt.Printf("\nAdd this to your config.yaml:\n")
	fmt.Printf("api:\n")
	fmt.Printf("  authSecret: \"%s\"\n", secret)
	fmt.Printf("\nOr set environment variable:\n")
	fmt.Printf("export API_AUTH_SECRET=\"%s\"\n", secret)
}

// Helper function to parse duration with additional formats
func parseDurationWithDays(s string) (time.Duration, error) {
	// Handle day format (e.g., "7d", "30d")
	if len(s) > 1 && s[len(s)-1] == 'd' {
		days, err := strconv.Atoi(s[:len(s)-1])
		if err != nil {
			return 0, err
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}

	// Use standard time.ParseDuration for other formats
	return time.ParseDuration(s)
}
