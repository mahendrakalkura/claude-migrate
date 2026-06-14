package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

type histLine struct {
	Display        json.RawMessage `json:"display"`
	PastedContents json.RawMessage `json:"pastedContents"`
	Timestamp      json.RawMessage `json:"timestamp"`
	Project        string          `json:"project"`
	SessionId      json.RawMessage `json:"sessionId"`
}

var nonAlphanumeric = regexp.MustCompile(`[^a-zA-Z0-9]`)

func main() {
	err := run(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	if len(arguments) != 2 {
		return fmt.Errorf("usage: claude-migrate <src> <dst>")
	}

	running, err := claudeRunning()
	if err != nil {
		return err
	}
	if running {
		return fmt.Errorf("a claude process is running; close all Claude sessions first")
	}

	directory, err := configDir()
	if err != nil {
		return err
	}
	projects := filepath.Join(directory, "projects")
	history := filepath.Join(directory, "history.jsonl")

	source, err := canonicalizeSrc(arguments[0])
	if err != nil {
		return err
	}
	destination, err := canonicalizeDst(arguments[1])
	if err != nil {
		return err
	}

	sourceFolder := encode(source)
	destinationFolder := encode(destination)
	sourceSession := filepath.Join(projects, sourceFolder)
	destinationSession := filepath.Join(projects, destinationFolder)

	err = preconditions(source, destination, sourceSession, destinationSession)
	if err != nil {
		return err
	}

	transcripts, err := countTranscripts(sourceSession)
	if err != nil {
		return err
	}
	lines, err := countHistoryLines(history, source)
	if err != nil {
		return err
	}

	fmt.Println("claude-migrate")
	fmt.Println()
	fmt.Printf("  %-15s %s  ->  %s\n", "project", source, destination)
	fmt.Printf("  %-15s %s  ->  %s\n", "session folder", sourceFolder, destinationFolder)
	fmt.Printf("  %-15s %s\n", "transcripts", plural(transcripts, "file"))
	fmt.Printf("  %-15s %s\n", "history.jsonl", plural(lines, "line"))
	fmt.Println()

	proceed, err := confirm()
	if err != nil {
		return err
	}
	if !proceed {
		return nil
	}

	err = copyTree(source, destination)
	if err != nil {
		cleanup(destination, destinationSession)
		return err
	}
	err = copyTree(sourceSession, destinationSession)
	if err != nil {
		cleanup(destination, destinationSession)
		return err
	}
	err = rewriteHistory(history, source, destination)
	if err != nil {
		cleanup(destination, destinationSession)
		return err
	}

	err = os.RemoveAll(source)
	if err != nil {
		return err
	}
	err = os.RemoveAll(sourceSession)
	if err != nil {
		return err
	}
	return nil
}

func canonicalizeDst(input string) (string, error) {
	absolute, err := filepath.Abs(input)
	if err != nil {
		return "", err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(absolute))
	if err != nil {
		return "", fmt.Errorf("destination parent directory does not exist: %s", filepath.Dir(absolute))
	}
	resolved := filepath.Join(parent, filepath.Base(absolute))
	return resolved, nil
}

func canonicalizeSrc(input string) (string, error) {
	absolute, err := filepath.Abs(input)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("source does not exist: %s", absolute)
	}
	return resolved, nil
}

func claudeRunning() (bool, error) {
	if runtime.GOOS == "windows" {
		output, err := exec.Command("tasklist", "/FI", "IMAGENAME eq claude.exe", "/NH").Output()
		if err != nil {
			return false, err
		}
		return strings.Contains(string(output), "claude.exe"), nil
	}
	err := exec.Command("pgrep", "-x", "claude").Run()
	if err == nil {
		return true, nil
	}
	exitError := &exec.ExitError{}
	if errors.As(err, &exitError) && exitError.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

func cleanup(destination, destinationSession string) {
	err := os.RemoveAll(destination)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not remove %s: %v\n", destination, err)
	}
	err = os.RemoveAll(destinationSession)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not remove %s: %v\n", destinationSession, err)
	}
}

func confirm() (bool, error) {
	fmt.Print("Proceed? [y/N] ")
	reader := bufio.NewReader(os.Stdin)
	answer, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return false, err
	}
	return strings.TrimSpace(answer) == "y", nil
}

func configDir() (string, error) {
	directory := os.Getenv("CLAUDE_CONFIG_DIR")
	if directory != "" {
		return directory, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude"), nil
}

func copyFile(source, target string, info fs.FileInfo) (err error) {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() {
		e := input.Close()
		if err == nil {
			err = e
		}
	}()
	output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return err
	}
	_, err = io.Copy(output, input)
	if err != nil {
		e := output.Close()
		if e != nil {
			return e
		}
		return err
	}
	err = output.Close()
	if err != nil {
		return err
	}
	err = os.Chtimes(target, info.ModTime(), info.ModTime())
	return err
}

func copySymlink(source, target string) error {
	link, err := os.Readlink(source)
	if err != nil {
		return err
	}
	return os.Symlink(link, target)
}

func copyTree(source, destination string) error {
	directoryTimes := map[string]time.Time{}
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		switch {
		case entry.Type()&fs.ModeSymlink != 0:
			return copySymlink(path, target)
		case entry.IsDir():
			err := os.MkdirAll(target, info.Mode().Perm())
			if err != nil {
				return err
			}
			directoryTimes[target] = info.ModTime()
			return nil
		case info.Mode().IsRegular():
			return copyFile(path, target, info)
		default:
			return fmt.Errorf("cannot copy special file: %s", path)
		}
	})
	if err != nil {
		return err
	}
	for directory, modTime := range directoryTimes {
		err := os.Chtimes(directory, modTime, modTime)
		if err != nil {
			return err
		}
	}
	return nil
}

func countHistoryLines(history, source string) (int, error) {
	input, err := os.Open(history)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	defer func() {
		e := input.Close()
		if e != nil && err == nil {
			err = e
		}
	}()
	count := 0
	reader := bufio.NewReader(input)
	for {
		line, e := reader.ReadString('\n')
		if len(line) > 0 {
			record := histLine{}
			parsed := json.Unmarshal([]byte(strings.TrimSuffix(line, "\n")), &record)
			if parsed == nil && record.Project == source {
				count++
			}
		}
		if e != nil {
			if e == io.EOF {
				break
			}
			return 0, e
		}
	}
	return count, nil
}

func countTranscripts(sourceSession string) (int, error) {
	entries, err := os.ReadDir(sourceSession)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".jsonl") {
			count++
		}
	}
	return count, nil
}

func encode(path string) string {
	return nonAlphanumeric.ReplaceAllString(path, "-")
}

func plural(count int, noun string) string {
	if count == 1 {
		return fmt.Sprintf("%d %s", count, noun)
	}
	return fmt.Sprintf("%d %ss", count, noun)
}

func preconditions(source, destination, sourceSession, destinationSession string) error {
	info, err := os.Stat(source)
	if err != nil {
		return fmt.Errorf("source does not exist: %s", source)
	}
	if !info.IsDir() {
		return fmt.Errorf("source is not a directory: %s", source)
	}

	_, err = os.Stat(sourceSession)
	if err != nil {
		return fmt.Errorf("source session folder does not exist: %s", sourceSession)
	}

	_, err = os.Lstat(destination)
	if err == nil {
		return fmt.Errorf("destination already exists: %s", destination)
	}

	_, err = os.Lstat(destinationSession)
	if err == nil {
		return fmt.Errorf("destination session folder already exists: %s", destinationSession)
	}

	if source == destination {
		return fmt.Errorf("source and destination are the same path: %s", source)
	}
	if within(source, destination) {
		return fmt.Errorf("destination is nested inside source: %s", destination)
	}
	if within(destination, source) {
		return fmt.Errorf("source is nested inside destination: %s", source)
	}
	return nil
}

func rewriteHistory(history, source, destination string) (err error) {
	input, err := os.Open(history)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer func() {
		e := input.Close()
		if e != nil && err == nil {
			err = e
		}
	}()

	temporary, err := os.CreateTemp(filepath.Dir(history), "history.jsonl.*")
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		e := temporary.Close()
		if e != nil && err == nil {
			err = e
		}
		e = os.Remove(temporary.Name())
		if e != nil && err == nil {
			err = e
		}
	}()

	writer := bufio.NewWriter(temporary)
	reader := bufio.NewReader(input)
	for {
		line, e := reader.ReadString('\n')
		if len(line) > 0 {
			content := strings.TrimSuffix(line, "\n")
			rewritten := rewriteLine([]byte(content), source, destination)
			_, err = writer.Write(rewritten)
			if err != nil {
				return err
			}
			if strings.HasSuffix(line, "\n") {
				err = writer.WriteByte('\n')
				if err != nil {
					return err
				}
			}
		}
		if e != nil {
			if e == io.EOF {
				break
			}
			return e
		}
	}

	err = writer.Flush()
	if err != nil {
		return err
	}
	err = temporary.Close()
	if err != nil {
		return err
	}
	err = os.Rename(temporary.Name(), history)
	if err != nil {
		return err
	}
	committed = true
	return nil
}

func rewriteLine(line []byte, source, destination string) []byte {
	record := histLine{}
	err := json.Unmarshal(line, &record)
	if err != nil {
		return line
	}
	if record.Project != source {
		return line
	}
	record.Project = destination
	buffer := &bytes.Buffer{}
	encoder := json.NewEncoder(buffer)
	encoder.SetEscapeHTML(false)
	err = encoder.Encode(record)
	if err != nil {
		return line
	}
	return bytes.TrimRight(buffer.Bytes(), "\n")
}

func within(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	if relative == "." {
		return false
	}
	return !strings.HasPrefix(relative, "..")
}
