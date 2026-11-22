package cfc

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"go.mau.fi/whatsmeow"
	waCommon "go.mau.fi/whatsmeow/proto/waCommon"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	waGroupHistory "go.mau.fi/whatsmeow/proto/waGroupHistory"
	waWeb "go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

// Spec satu pesan palsu dalam history bundle.
type Spec struct {
	Participant string
	Timestamp   int64
	Text        string
	FromMe      bool
	PushName    string
	MessageID   string
	QuotedID    string
}

// Send membangun bundle group-history (protobuf terkompresi) dan mengirimnya
// ke groupJid yang dialamatkan ke targetJid. Eksperimental: bergantung pada
// perilaku server WhatsApp; tidak ada garansi render identik dengan CFC Baileys.
func Send(ctx context.Context, client *whatsmeow.Client, groupJidStr, targetJidStr string, specs []Spec) error {
	groupJid, err := types.ParseJID(groupJidStr)
	if err != nil {
		return fmt.Errorf("group JID invalid: %w", err)
	}
	targetJid, err := types.ParseJID(targetJidStr)
	if err != nil {
		return fmt.Errorf("target JID invalid: %w", err)
	}
	if len(specs) == 0 {
		return fmt.Errorf("minimal satu pesan")
	}

	preps, byID, oldest := prepareSpecs(specs)
	msgs := buildMessages(preps, byID, groupJid)

	compressed, err := compressHistory(msgs)
	if err != nil {
		return err
	}

	resp, err := client.Upload(ctx, compressed, whatsmeow.MediaHistory)
	if err != nil {
		return fmt.Errorf("gagal upload group-history: %w", err)
	}

	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return fmt.Errorf("gagal random secret: %w", err)
	}

	bundle := &waE2E.Message{
		MessageHistoryBundle: &waE2E.MessageHistoryBundle{
			Mimetype:          proto.String("application/protobuf"),
			FileSHA256:        resp.FileSHA256,
			MediaKey:          resp.MediaKey,
			FileEncSHA256:     resp.FileEncSHA256,
			DirectPath:        proto.String(resp.DirectPath),
			MediaKeyTimestamp: proto.Int64(time.Now().Unix()),
			MessageHistoryMetadata: &waE2E.MessageHistoryMetadata{
				HistoryReceivers:               []string{targetJid.String()},
				NonHistoryReceivers:            []string{},
				MessageCount:                   proto.Int64(int64(len(msgs))),
				OldestMessageTimestampInWindow: proto.Int64(oldest),
				OldestMessageTimestampInBundle: proto.Int64(oldest),
			},
		},
		MessageContextInfo: &waE2E.MessageContextInfo{
			MessageSecret: secret,
		},
	}

	if _, err := client.SendMessage(ctx, groupJid, bundle); err != nil {
		return fmt.Errorf("gagal kirim bundle: %w", err)
	}
	return nil
}

type preparedSpec struct {
	spec        Spec
	id          string
	ts          int64
	participant string
}

func prepareSpecs(specs []Spec) ([]preparedSpec, map[string]int, int64) {
	preps := make([]preparedSpec, len(specs))
	byID := make(map[string]int)
	var oldest int64
	for i, s := range specs {
		ts := s.Timestamp
		if ts == 0 {
			ts = time.Now().Unix() - 3600
		}
		if oldest == 0 || ts < oldest {
			oldest = ts
		}
		msgID := s.MessageID
		if msgID == "" {
			b := make([]byte, 9)
			_, _ = rand.Read(b)
			msgID = "3EB0" + hex.EncodeToString(b)
		}
		participant := s.Participant
		if participant == "" {
			participant = "0@s.whatsapp.net"
		}
		preps[i] = preparedSpec{spec: s, id: msgID, ts: ts, participant: participant}
		byID[msgID] = i
	}
	return preps, byID, oldest
}

// Quoted messages that exist inside the bundle are resolved to their generated ID.
func buildMessages(preps []preparedSpec, byID map[string]int, groupJid types.JID) []*waWeb.WebMessageInfo {
	msgs := make([]*waWeb.WebMessageInfo, 0, len(preps))
	for _, p := range preps {
		s := p.spec
		etm := &waE2E.ExtendedTextMessage{Text: proto.String(s.Text)}
		if s.QuotedID != "" {
			if qi, ok := byID[s.QuotedID]; ok {
				qp := preps[qi].participant
				etm.ContextInfo = &waE2E.ContextInfo{
					StanzaID:      proto.String(preps[qi].id),
					Participant:   proto.String(qp),
					QuotedMessage: &waE2E.Message{Conversation: proto.String(preps[qi].spec.Text)},
				}
			} else {
				etm.ContextInfo = &waE2E.ContextInfo{
					StanzaID:    proto.String(s.QuotedID),
					Participant: proto.String(s.Participant),
				}
			}
		}
		wmi := &waWeb.WebMessageInfo{
			Key: &waCommon.MessageKey{
				RemoteJID:   proto.String(groupJid.String()),
				FromMe:      proto.Bool(s.FromMe),
				ID:          proto.String(p.id),
				Participant: proto.String(p.participant),
			},
			Message: &waE2E.Message{
				ExtendedTextMessage: etm,
			},
			MessageTimestamp: proto.Uint64(uint64(p.ts)),
			Participant:      proto.String(p.participant),
			Status:           func() *waWeb.WebMessageInfo_Status { v := waWeb.WebMessageInfo_SERVER_ACK; return &v }(),
		}
		if s.PushName != "" {
			wmi.PushName = proto.String(s.PushName)
		}
		msgs = append(msgs, wmi)
	}

	// Baileys materializes messages in reverse insertion order.
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs
}

func compressHistory(msgs []*waWeb.WebMessageInfo) ([]byte, error) {
	serialized, err := proto.Marshal(&waGroupHistory.GroupHistory{Messages: msgs})
	if err != nil {
		return nil, fmt.Errorf("gagal marshal group history: %w", err)
	}

	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	if _, err := zw.Write(serialized); err != nil {
		return nil, fmt.Errorf("gagal kompresi: %w", err)
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("gagal tutup zlib: %w", err)
	}
	return buf.Bytes(), nil
}
