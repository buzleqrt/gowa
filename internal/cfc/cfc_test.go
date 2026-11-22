package cfc

import (
	"strings"
	"testing"

	"go.mau.fi/whatsmeow/types"
)

func TestBuildMessagesOrderAndQuote(t *testing.T) {
	specs := []Spec{
		{Text: "halo", MessageID: "A"},
		{Text: "balas", MessageID: "B", QuotedID: "A"},
	}

	preps, byID, oldest := prepareSpecs(specs)
	if oldest == 0 {
		t.Fatal("oldest timestamp tidak diisi")
	}
	if len(byID) != 2 {
		t.Fatalf("byID berisi %d pesan, harus 2", len(byID))
	}
	if preps[0].participant != "0@s.whatsapp.net" {
		t.Fatalf("participant default salah: %q", preps[0].participant)
	}

	group := types.NewJID("1234", types.GroupServer)
	msgs := buildMessages(preps, byID, group)
	if len(msgs) != 2 {
		t.Fatalf("jumlah pesan %d, harus 2", len(msgs))
	}

	first := msgs[0]
	if id := first.GetKey().GetID(); id != "B" {
		t.Fatalf("pesan pertama harus B, dapat %q", id)
	}
	if jid := first.GetKey().GetRemoteJID(); jid != group.String() {
		t.Fatalf("RemoteJID %q, harus %q", jid, group.String())
	}
	if stanza := first.GetMessage().GetExtendedTextMessage().GetContextInfo().GetStanzaID(); stanza != "A" {
		t.Fatalf("quote harus menunjuk ke A, dapat %q", stanza)
	}
	if quoted := first.GetMessage().GetExtendedTextMessage().GetContextInfo().GetQuotedMessage().GetConversation(); quoted != "halo" {
		t.Fatalf("teks quote salah: %q", quoted)
	}
}

func TestPrepareSpecsGeneratesID(t *testing.T) {
	preps, _, _ := prepareSpecs([]Spec{{Text: "tanpa id"}})
	if !strings.HasPrefix(preps[0].id, "3EB0") || len(preps[0].id) != 22 {
		t.Fatalf("ID hasil generate tidak sesuai format: %q", preps[0].id)
	}
}
