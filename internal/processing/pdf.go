package processing

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"
)

func ExtractTextFromPDF(pdfPath string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "pdftotext", "-layout", pdfPath, "-")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("pdftotext execution failed: %w: %s", err, stderr.String())
	}

	return stdout.String(), nil
}
