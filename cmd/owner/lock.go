package owner

import (
	"fmt"
	"strings"

	"gowa/internal/command"
)

func init() {
	command.Register(&command.Command{
		Name:    "lock",
		Cmd:     "lock",
		Categ:   "owner",
		Desc:    "Kunci atau buka bot sementara (Khusus Owner)",
		Alias:   []string{"unlock"},
		Execute: handleLock,
	})
}

func handleLock(ctx *command.Context) error {
	if !ctx.IsOwner {
		return nil
	}

	botName := strings.ToLower(strings.TrimSpace(ctx.Config.BotName))
	keyword := ""
	if len(ctx.Args) > 0 {
		keyword = strings.ToLower(strings.TrimSpace(ctx.Args[0]))
	}

	if keyword != botName {
		return ctx.Reply(fmt.Sprintf(
			"*Status:* %s\n\n_Gunakan_ `%slock %s` _atau_ `%sunlock %s`.",
			lockStatus(),
			ctx.BotPrefix,
			ctx.Config.BotName,
			ctx.BotPrefix,
			ctx.Config.BotName,
		))
	}

	unlock := strings.EqualFold(ctx.Command, "unlock")

	if !unlock {
		if command.IsLocked() {
			return ctx.Reply("*Bot sudah terkunci.*")
		}
		command.SetLocked(true)
		return ctx.Reply("*Bot dikunci.* Hanya `.unlock` yang diproses.")
	}

	if !command.IsLocked() {
		return ctx.Reply("*Bot sudah terbuka.*")
	}
	command.SetLocked(false)
	return ctx.Reply("*Bot dibuka.* Semua perintah aktif kembali.")
}

func lockStatus() string {
	if command.IsLocked() {
		return "`locked`"
	}
	return "`unlocked`"
}
