package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type archiveSnapshot struct {
	Meta    archiveMeta
	Records []Record
}

func readArchiveSnapshot(dir string) (*archiveSnapshot, error) {
	s := &archiveSnapshot{}
	if err := readArchiveMeta(dir, &s.Meta); err != nil {
		return nil, err
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil {
		return nil, err
	}
	for _, path := range files {
		peer := strings.TrimSuffix(filepath.Base(path), ".jsonl")
		if !validPeer.MatchString(peer) {
			return nil, errors.New("invalid JSONL filename")
		}
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 4096), 4*1024*1024)
		for scanner.Scan() {
			var r Record
			if err := json.Unmarshal(scanner.Bytes(), &r); err != nil {
				_ = f.Close()
				return nil, fmt.Errorf("invalid JSONL in %s", peer)
			}
			if r.Peer != peer || r.AccountID != s.Meta.AccountID || r.MessageID <= 0 {
				_ = f.Close()
				return nil, errors.New("archive identity mismatch")
			}
			s.Records = append(s.Records, r)
		}
		err = scanner.Err()
		_ = f.Close()
		if err != nil {
			return nil, err
		}
	}
	// Archive files are replaced atomically. Re-read tombstones after opening them
	// so a deletion committed while we scanned does not appear in search results.
	if err := readArchiveMeta(dir, &s.Meta); err != nil {
		return nil, err
	}
	filtered := s.Records[:0]
	for _, r := range s.Records {
		if r.AccountID != s.Meta.AccountID {
			return nil, errors.New("archive account changed during search")
		}
		if s.Meta.Blocked[r.Peer] != "" || s.Meta.Deleted[r.Peer][r.MessageID] || (!strings.HasPrefix(r.Peer, "channel-") && s.Meta.NonChannelDeleted[r.MessageID]) {
			continue
		}
		if _, ok := s.Meta.Peers[r.Peer]; !ok {
			return nil, errors.New("archive peer metadata missing")
		}
		filtered = append(filtered, r)
	}
	s.Records = filtered
	return s, nil
}

func matchesRecord(re *regexp.Regexp, r Record) bool {
	fields := []string{r.Text, r.Peer, strconv.Itoa(r.MessageID), r.MediaType, r.MessageURL, r.AlbumID}
	if m := r.Media; m != nil {
		fields = append(fields, m.Kind)
		if f := m.File; f != nil {
			fields = append(fields, f.Name, f.MIME, f.Title, f.Performer, f.ID)
		}
		if p := m.Poll; p != nil {
			fields = append(fields, p.Question, p.Solution)
			for _, a := range p.Options {
				fields = append(fields, a.Text)
			}
		}
		b, err := json.Marshal(m)
		if err == nil {
			fields = append(fields, string(b))
		}
	}
	for _, field := range fields {
		if re.MatchString(field) {
			return true
		}
	}
	return false
}

func (s *archiveSnapshot) search(pattern, peer string, limit int) ([]Record, error) {
	if pattern == "" || limit < 1 || limit > 1000 {
		return nil, errors.New("require a nonempty --pattern and --limit from 1 to 1000")
	}
	if peer != "" && !validPeer.MatchString(peer) {
		return nil, errors.New("peer must be user-ID, chat-ID, or channel-ID")
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid regular expression: %w", err)
	}
	var results []Record
	for _, r := range s.Records {
		if (peer == "" || r.Peer == peer) && matchesRecord(re, r) {
			results = append(results, r)
		}
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Date.Equal(results[j].Date) {
			if results[i].Peer == results[j].Peer {
				return results[i].MessageID > results[j].MessageID
			}
			return results[i].Peer < results[j].Peer
		}
		return results[i].Date.After(results[j].Date)
	})
	if len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

func writeSearchResults(w io.Writer, results []Record) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	for _, r := range results {
		if err := enc.Encode(r); err != nil {
			return err
		}
	}
	return nil
}

func runSearch(args []string) error {
	fs := flag.NewFlagSet("telegram-jsonl search", flag.ContinueOnError)
	dir := fs.String("archive", "./data-tdl/archive", "local archive directory (read only)")
	pattern := fs.String("pattern", "", "Go regular expression for message text, filenames, types, or structured fields")
	peer := fs.String("peer", "", "optional peer filter, e.g. channel-123")
	limit := fs.Int("limit", 20, "maximum results, newest first (1-1000)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	s, err := readArchiveSnapshot(*dir)
	if err != nil {
		return err
	}
	results, err := s.search(*pattern, *peer, *limit)
	if err != nil {
		return err
	}
	return writeSearchResults(os.Stdout, results)
}
