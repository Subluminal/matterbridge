package bunreal

import (
    "regexp"
    "github.com/Subluminal/matterbridge/bridge/config"
    "github.com/Subluminal/matterbridge/bridge/unreal/unreal"
    log "github.com/Sirupsen/logrus"
)

type bunreal struct {
    s       *unreal.Server
    Config  *config.Protocol
    Remote  chan config.Message
    Account string
}

var flog *log.Entry
var protocol = "unreal"

func init() {
    flog = log.WithFields(log.Fields{"module": protocol})
}

func New(cfg config.Protocol, account string, c chan config.Message) *bunreal {
    b := &bunreal{}
    b.Config = &cfg
    b.Remote = c
    b.Account = account
    return b
}

func (b *bunreal) Connect() error {
    flog.Infof("Connecting to %s", b.Config.Server)
    s := unreal.New(b.Config.Name, b.Config.Sid, b.Config.Password, b.Config.Nick, b.handlePrivmsg)
    err := s.Connect(b.Config.Server)
    if err != nil {
        return err
    }
    b.s = s
    return nil
}

func (b *bunreal) Disconnect() error {
	return nil
}

func (b *bunreal) JoinChannel(channel config.ChannelInfo) error {
    b.s.Join(channel.Name)
    return nil
}

func (b *bunreal) Send(msg config.Message) (string, error) {
    flog.Debugf("Receiving %#v", msg)
    re := regexp.MustCompile(`[[:cntrl:]]`)
    text := re.ReplaceAllString(msg.Text, "")
    b.s.Send(msg.Channel, msg.Username, text, msg.Account)
    return "", nil
}

func (b *bunreal) handlePrivmsg(nick string, channel string, text string) {
    // strip IRC colors
    re := regexp.MustCompile(`[[:cntrl:]](\d+,|)\d+`)
    msg := re.ReplaceAllString(text, "")
    flog.Debugf("Sending message from %s on %s to gateway", nick, b.Account)
    b.Remote <- config.Message{Username: nick, Text: msg, Channel: channel, Account: b.Account}
}
