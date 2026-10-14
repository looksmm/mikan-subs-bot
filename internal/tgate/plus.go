package tgate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
)

// The bot's additions to the client (not in mikan): the look of what the bot sends.

// Decorate sets what dresses the body of every call before it goes: dress gets the method
// and the JSON body and gives the body to send instead, ok false to send it as it is.
// refused hears of a dressed body Telegram turned down; the plain one went in its place.
// Set it before the client is used.
func (c *Client) Decorate(dress func(method string, body []byte) ([]byte, bool), refused func(method string, err error)) {
	c.decorate, c.refused = dress, refused
}

// Refused: Telegram turned the request down as bad (400), for a reason the look may have
// caused. An edit to the same content, or of a message that is gone, is not one: sending the
// plain body would only change what the chat already shows.
func Refused(err error) bool {
	var ae *APIError
	if !errors.As(err, &ae) || ae.Code != 400 {
		return false
	}
	d := strings.ToLower(ae.Description)
	for _, s := range []string{"message is not modified", "message to edit not found", "message can't be edited",
		"message to delete not found", "chat not found", "query is too old"} {
		if strings.Contains(d, s) {
			return false
		}
	}
	return true
}

type priorityKey struct{}

// WithPriority marks the calls made with ctx for the lane of the panel's outbox they belong
// in: "notice" or "bulk" (a broadcast). The Bot API proxy sends replies before them.
func WithPriority(ctx context.Context, p string) context.Context {
	return context.WithValue(ctx, priorityKey{}, p)
}

func priorityOf(ctx context.Context) string {
	p, _ := ctx.Value(priorityKey{}).(string)
	return p
}

// stripLook is a JSON body without the buttons' icons and colours and the text's custom
// emoji; ok is false when it had none.
func stripLook(body []byte) ([]byte, bool) {
	if !bytes.Contains(body, []byte(`"icon_custom_emoji_id"`)) && !bytes.Contains(body, []byte(`"style"`)) &&
		!bytes.Contains(body, []byte(`tg-emoji`)) {
		return nil, false
	}
	var in map[string]any
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if dec.Decode(&in) != nil {
		return nil, false
	}
	changed := false
	if rm, ok := in["reply_markup"].(map[string]any); ok {
		rows, _ := rm["inline_keyboard"].([]any)
		for _, r := range rows {
			row, _ := r.([]any)
			for _, x := range row {
				if btn, ok := x.(map[string]any); ok {
					for _, k := range []string{"icon_custom_emoji_id", "style"} {
						if _, has := btn[k]; has {
							delete(btn, k)
							changed = true
						}
					}
				}
			}
		}
	}
	for _, f := range []string{"text", "caption"} {
		if t, ok := in[f].(string); ok && strings.Contains(t, "<tg-emoji") {
			in[f] = tgEmoji.ReplaceAllString(t, "$1")
			changed = true
		}
	}
	if !changed {
		return nil, false
	}
	out, err := json.Marshal(in)
	return out, err == nil
}

var tgEmoji = regexp.MustCompile(`<tg-emoji[^>]*>(.*?)</tg-emoji>`)

// SendPhoto posts a picture by its address (Telegram fetches it) with an HTML caption and
// buttons; a caption Telegram cannot parse goes as plain text.
func (c *Client) SendPhoto(ctx context.Context, chat int64, photo, caption string, kb *Keyboard, silent bool) (Message, error) {
	var m Message
	in := map[string]any{"chat_id": chat, "photo": photo, "caption": caption, "parse_mode": "HTML"}
	if silent {
		in["disable_notification"] = true
	}
	if kb != nil {
		in["reply_markup"] = kb
	}
	err := c.call(ctx, "sendPhoto", in, &m)
	if unparsable(err) {
		delete(in, "parse_mode")
		in["caption"] = PlainText(caption)
		err = c.call(ctx, "sendPhoto", in, &m)
	}
	return m, err
}

// OnGone sets who hears of a chat that blocked the bot (a 403 to a send). Set it before
// the outbox runs.
func (o *Outbox) OnGone(f func(chat int64)) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.gone = f
}
