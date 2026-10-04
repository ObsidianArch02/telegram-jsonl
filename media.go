package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/gotd/td/tg"
)

const maxMediaBytes = 32 * 1024
const maxVCardBytes = 8 * 1024

type MediaDetails struct {
	Kind           string            `json:"kind"`
	File           *FileMetadata     `json:"file,omitempty"`
	Poll           *PollMetadata     `json:"poll,omitempty"`
	Location       *LocationMetadata `json:"location,omitempty"`
	Venue          *VenueMetadata    `json:"venue,omitempty"`
	Contact        *ContactMetadata  `json:"contact,omitempty"`
	WebPage        *WebPageMetadata  `json:"webpage,omitempty"`
	Dice           *DiceMetadata     `json:"dice,omitempty"`
	Omitted        []string          `json:"omitted_fields,omitempty"`
	OmissionReason string            `json:"omission_reason,omitempty"`
}

type FileMetadata struct {
	ID           string  `json:"id"`
	MIME         string  `json:"mime_type,omitempty"`
	Name         string  `json:"name,omitempty"`
	Size         int64   `json:"size_bytes,omitempty"`
	Width        int     `json:"width,omitempty"`
	Height       int     `json:"height,omitempty"`
	Duration     float64 `json:"duration_seconds,omitempty"`
	Title        string  `json:"title,omitempty"`
	Performer    string  `json:"performer,omitempty"`
	StickerEmoji string  `json:"sticker_emoji,omitempty"`
}

type PollMetadata struct {
	ID             string       `json:"id"`
	Question       string       `json:"question"`
	Options        []PollOption `json:"options"`
	Closed         bool         `json:"closed"`
	PublicVoters   bool         `json:"public_voters"`
	MultipleChoice bool         `json:"multiple_choice"`
	Quiz           bool         `json:"quiz"`
	ClosePeriod    int          `json:"close_period_seconds,omitempty"`
	CloseDate      int          `json:"close_date_unix,omitempty"`
	TotalVoters    *int         `json:"total_voters,omitempty"`
	PartialResults bool         `json:"partial_results"`
	Solution       string       `json:"solution,omitempty"`
}

type PollOption struct {
	Text    string `json:"text"`
	Option  string `json:"option_base64"`
	Voters  *int   `json:"voters,omitempty"`
	Chosen  *bool  `json:"chosen_by_account,omitempty"`
	Correct *bool  `json:"correct,omitempty"`
}

type LocationMetadata struct {
	Latitude        float64 `json:"latitude"`
	Longitude       float64 `json:"longitude"`
	Accuracy        int     `json:"accuracy_radius_m,omitempty"`
	Live            bool    `json:"live"`
	Period          int     `json:"live_period_seconds,omitempty"`
	Heading         int     `json:"heading_degrees,omitempty"`
	ProximityRadius int     `json:"proximity_radius_m,omitempty"`
}

type VenueMetadata struct {
	Title    string `json:"title"`
	Address  string `json:"address"`
	Provider string `json:"provider,omitempty"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
}

type ContactMetadata struct {
	Phone     string `json:"phone"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name,omitempty"`
	UserID    string `json:"user_id,omitempty"`
	VCard     string `json:"vcard,omitempty"`
}

type WebPageMetadata struct {
	URL         string `json:"url"`
	DisplayURL  string `json:"display_url,omitempty"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	SiteName    string `json:"site_name,omitempty"`
	Author      string `json:"author,omitempty"`
	Type        string `json:"type,omitempty"`
}

type DiceMetadata struct {
	Emoji string `json:"emoji"`
	Value int    `json:"value"`
}

// This is a Telegram message permalink, not a public attachment download URL.
func messageURL(m *tg.Message) string {
	p, ok := m.PeerID.(*tg.PeerChannel)
	if !ok || p.ChannelID <= 0 || m.ID <= 0 {
		return ""
	}
	link := fmt.Sprintf("https://t.me/c/%d/%d", p.ChannelID, m.ID)
	if m.GroupedID != 0 {
		link += "?single"
	}
	return link
}

func locationMetadata(point tg.GeoPointClass) *LocationMetadata {
	p, ok := point.(*tg.GeoPoint)
	if !ok {
		return nil
	}
	return &LocationMetadata{Latitude: p.Lat, Longitude: p.Long, Accuracy: p.AccuracyRadius}
}

func pollMetadata(p tg.Poll) *PollMetadata {
	m := &PollMetadata{ID: strconv.FormatInt(p.ID, 10), Question: p.Question.Text, Closed: p.Closed, PublicVoters: p.PublicVoters, MultipleChoice: p.MultipleChoice, Quiz: p.Quiz, ClosePeriod: p.ClosePeriod, CloseDate: p.CloseDate, Options: make([]PollOption, 0, len(p.Answers))}
	for _, answer := range p.Answers {
		m.Options = append(m.Options, PollOption{Text: answer.Text.Text, Option: base64.StdEncoding.EncodeToString(answer.Option)})
	}
	return m
}

func applyPollResults(p *PollMetadata, result tg.PollResults) {
	p.PartialResults = result.Min
	if n, ok := result.GetTotalVoters(); ok {
		p.TotalVoters = &n
	}
	if text, ok := result.GetSolution(); ok {
		p.Solution = text
	}
	for _, count := range result.Results {
		option := base64.StdEncoding.EncodeToString(count.Option)
		for i := range p.Options {
			if p.Options[i].Option != option {
				continue
			}
			n := count.Voters
			p.Options[i].Voters = &n
			// A min result omits account-specific choice; it must not erase it.
			if !result.Min {
				chosen, correct := count.Chosen, count.Correct
				p.Options[i].Chosen = &chosen
				p.Options[i].Correct = &correct
			}
		}
	}
}

func limitMedia(details *MediaDetails) *MediaDetails {
	if details == nil {
		return nil
	}
	b, err := json.Marshal(details)
	if err != nil || len(b) > maxMediaBytes {
		return &MediaDetails{Kind: details.Kind, OmissionReason: "structured metadata exceeds 32 KiB or cannot be encoded"}
	}
	return details
}

func mediaDetails(media tg.MessageMediaClass) *MediaDetails {
	var d *MediaDetails
	switch m := media.(type) {
	case *tg.MessageMediaPhoto:
		photo, ok := m.Photo.(*tg.Photo)
		if !ok {
			return nil
		}
		f := &FileMetadata{ID: strconv.FormatInt(photo.ID, 10), MIME: "image/jpeg"}
		for _, size := range photo.Sizes {
			var width, height int
			var bytes int64
			switch s := size.(type) {
			case *tg.PhotoSize:
				width, height, bytes = s.W, s.H, int64(s.Size)
			case *tg.PhotoSizeProgressive:
				width, height = s.W, s.H
				for _, n := range s.Sizes {
					bytes = max(bytes, int64(n))
				}
			}
			if int64(width)*int64(height) > int64(f.Width)*int64(f.Height) {
				f.Width, f.Height, f.Size = width, height, bytes
			}
		}
		d = &MediaDetails{Kind: "photo", File: f}
	case *tg.MessageMediaDocument:
		doc, ok := m.Document.(*tg.Document)
		if !ok {
			return nil
		}
		f := &FileMetadata{ID: strconv.FormatInt(doc.ID, 10), MIME: doc.MimeType, Size: doc.Size}
		kind := "document"
		audio, video, sticker, animated, round, voice := false, false, false, false, m.Round, m.Voice
		for _, attribute := range doc.Attributes {
			switch a := attribute.(type) {
			case *tg.DocumentAttributeFilename:
				f.Name = a.FileName
			case *tg.DocumentAttributeImageSize:
				f.Width, f.Height = a.W, a.H
			case *tg.DocumentAttributeAudio:
				audio = true
				voice = voice || a.Voice
				f.Duration = float64(a.Duration)
				f.Title, f.Performer = a.Title, a.Performer
			case *tg.DocumentAttributeVideo:
				video = true
				round = round || a.RoundMessage
				f.Duration = a.Duration
				f.Width, f.Height = a.W, a.H
			case *tg.DocumentAttributeSticker:
				sticker = true
				f.StickerEmoji = a.Alt
			case *tg.DocumentAttributeAnimated:
				animated = true
			}
		}
		switch {
		case sticker:
			kind = "sticker"
		case voice:
			kind = "voice"
		case round:
			kind = "video_note"
		case animated:
			kind = "animation"
		case video || m.Video:
			kind = "video"
		case audio:
			kind = "audio"
		}
		d = &MediaDetails{Kind: kind, File: f}
	case *tg.MessageMediaPoll:
		p := pollMetadata(m.Poll)
		applyPollResults(p, m.Results)
		d = &MediaDetails{Kind: "poll", Poll: p}
	case *tg.MessageMediaGeo:
		d = &MediaDetails{Kind: "location", Location: locationMetadata(m.Geo)}
	case *tg.MessageMediaGeoLive:
		p := locationMetadata(m.Geo)
		if p != nil {
			p.Live = true
			p.Period = m.Period
			p.Heading = m.Heading
			p.ProximityRadius = m.ProximityNotificationRadius
		}
		d = &MediaDetails{Kind: "live_location", Location: p}
	case *tg.MessageMediaVenue:
		d = &MediaDetails{Kind: "venue", Location: locationMetadata(m.Geo), Venue: &VenueMetadata{Title: m.Title, Address: m.Address, Provider: m.Provider, ID: m.VenueID, Type: m.VenueType}}
	case *tg.MessageMediaContact:
		c := &ContactMetadata{Phone: m.PhoneNumber, FirstName: m.FirstName, LastName: m.LastName}
		if m.UserID != 0 {
			c.UserID = strconv.FormatInt(m.UserID, 10)
		}
		d = &MediaDetails{Kind: "contact", Contact: c}
		if len(m.Vcard) <= maxVCardBytes {
			c.VCard = m.Vcard
		} else {
			d.Omitted = []string{"contact.vcard"}
		}
	case *tg.MessageMediaWebPage:
		page, ok := m.Webpage.(*tg.WebPage)
		if !ok {
			return nil
		}
		d = &MediaDetails{Kind: "webpage", WebPage: &WebPageMetadata{URL: page.URL, DisplayURL: page.DisplayURL, Title: page.Title, Description: page.Description, SiteName: page.SiteName, Author: page.Author, Type: page.Type}}
	case *tg.MessageMediaDice:
		d = &MediaDetails{Kind: "dice", Dice: &DiceMetadata{Emoji: m.Emoticon, Value: m.Value}}
	}
	return limitMedia(d)
}

func (a *archive) updatePoll(update *tg.UpdateMessagePoll) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.failure.check(); err != nil {
		return err
	}
	id := strconv.FormatInt(update.PollID, 10)
	for peer, records := range a.rows {
		changed := false
		for msgID, record := range records {
			if a.excluded(peer, msgID) || record.Media == nil || record.Media.Poll == nil || record.Media.Poll.ID != id {
				continue
			}
			previous := record
			details := *record.Media
			poll := *details.Poll
			poll.Options = append([]PollOption(nil), poll.Options...)
			if update.Poll.ID != 0 {
				updated := pollMetadata(update.Poll)
				updated.TotalVoters, updated.Solution, updated.PartialResults = poll.TotalVoters, poll.Solution, poll.PartialResults
				for i := range updated.Options {
					for _, old := range poll.Options {
						if old.Option == updated.Options[i].Option {
							updated.Options[i].Voters, updated.Options[i].Chosen, updated.Options[i].Correct = old.Voters, old.Chosen, old.Correct
						}
					}
				}
				poll = *updated
			}
			applyPollResults(&poll, update.Results)
			details.Poll = &poll
			record.Media = limitMedia(&details)
			record.Schema = 2
			a.touch(peer, msgID)
			if !recordsEqual(previous, record) {
				records[msgID] = record
				changed = true
			}
		}
		if changed {
			if err := a.flush(peer); err != nil {
				return err
			}
		}
	}
	return nil
}
