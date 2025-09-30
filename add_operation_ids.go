package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// convertFunctionNameToOperationID converts a function name like "APIEpochsV1" to "getEpochs"
func convertFunctionNameToOperationID(funcName string, httpMethod string) string {
	// Remove common prefixes and suffixes
	name := funcName
	name = strings.TrimPrefix(name, "API")
	name = strings.TrimPrefix(name, "Api")
	name = strings.TrimSuffix(name, "V1")
	name = strings.TrimSuffix(name, "Get")
	name = strings.TrimSuffix(name, "Post")

	// Determine verb based on HTTP method and function name
	var verb string
	switch strings.ToUpper(httpMethod) {
	case "GET":
		verb = "get"
	case "POST":
		// Check if the name contains action verbs
		lowerName := strings.ToLower(name)
		if strings.Contains(lowerName, "scan") {
			if strings.Contains(lowerName, "mass") {
				verb = "mass"
				name = strings.Replace(name, "Mass", "", 1)
			} else {
				verb = "scan"
				name = strings.Replace(name, "Scan", "", 1)
			}
		} else if strings.Contains(lowerName, "create") {
			verb = "create"
			name = strings.Replace(name, "Create", "", 1)
		} else if strings.Contains(lowerName, "submit") {
			verb = "submit"
			name = strings.Replace(name, "Submit", "", 1)
		} else {
			// Default to "get" for POST endpoints that retrieve data (like validator lookup)
			verb = "get"
		}
	case "PUT", "PATCH":
		verb = "update"
	case "DELETE":
		verb = "delete"
	default:
		verb = "get"
	}

	// Convert to camelCase: first letter lowercase
	if len(name) > 0 {
		name = strings.ToLower(string(name[0])) + name[1:]
	}

	// Build operation ID
	operationID := verb + strings.ToUpper(string(name[0])) + name[1:]

	return operationID
}

// processFile processes a single Go file and adds operation IDs
func processFile(filePath string) (bool, int, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return false, 0, err
	}
	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	if err := scanner.Err(); err != nil {
		return false, 0, err
	}

	// Process lines
	modified := false
	addedCount := 0
	routerRegex := regexp.MustCompile(`^//\s*@Router\s+\S+\s+\[(\w+)\]`)
	idRegex := regexp.MustCompile(`^//\s*@[Ii][Dd]\s+`)
	funcRegex := regexp.MustCompile(`^func\s+([A-Za-z0-9_]+)\s*\(`)

	inSwaggerBlock := false
	hasIDAnnotation := false
	httpMethod := ""
	var newLines []string

	for i := 0; i < len(lines); i++ {
		line := lines[i]

		// Check if we're in a swagger documentation block
		if strings.HasPrefix(strings.TrimSpace(line), "// @") {
			inSwaggerBlock = true
		}

		// Check for @ID annotation
		if idRegex.MatchString(line) {
			hasIDAnnotation = true
		}

		// Check for @Router annotation
		if matches := routerRegex.FindStringSubmatch(line); matches != nil {
			httpMethod = matches[1]
		}

		// Check for function declaration
		if matches := funcRegex.FindStringSubmatch(line); matches != nil {
			funcName := matches[1]

			// If we were in a swagger block, found a router, but no ID annotation
			if inSwaggerBlock && httpMethod != "" && !hasIDAnnotation {
				operationID := convertFunctionNameToOperationID(funcName, httpMethod)

				// Add the @ID annotation before the function declaration
				newLines = append(newLines, fmt.Sprintf("// @ID %s", operationID))
				fmt.Printf("  ✓ %s -> @ID %s\n", funcName, operationID)
				modified = true
				addedCount++
			}

			// Reset state
			inSwaggerBlock = false
			hasIDAnnotation = false
			httpMethod = ""
		}

		newLines = append(newLines, line)
	}

	// Write back if modified
	if modified {
		output := strings.Join(newLines, "\n")
		if err := os.WriteFile(filePath, []byte(output), 0644); err != nil {
			return false, 0, err
		}
	}

	return modified, addedCount, nil
}

func main() {
	handlersDir := "handlers/api"

	fmt.Println("=== Adding Operation IDs to API Handlers ===\n")

	totalFiles := 0
	modifiedFiles := 0
	totalHandlers := 0

	// Process all .go files in handlers/api
	err := filepath.Walk(handlersDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories and non-Go files
		if info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}

		// Skip handler.go and general.go
		baseName := filepath.Base(path)
		if baseName == "handler.go" || baseName == "general.go" {
			return nil
		}

		totalFiles++
		fmt.Printf("Processing: %s\n", path)

		modified, count, err := processFile(path)
		if err != nil {
			return fmt.Errorf("error processing %s: %w", path, err)
		}

		if modified {
			modifiedFiles++
			totalHandlers += count
			fmt.Println("  File updated")
		} else {
			fmt.Println("  No changes needed")
		}
		fmt.Println()

		return nil
	})

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Print summary
	fmt.Println("=== Summary ===")
	fmt.Printf("Total files processed: %d\n", totalFiles)
	fmt.Printf("Files modified: %d\n", modifiedFiles)
	fmt.Printf("Operation IDs added: %d\n", totalHandlers)
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Println("  1. Review the changes with: git diff")
	fmt.Println("  2. Regenerate swagger docs with: swag init")
	fmt.Println("  3. Test your API documentation")
}