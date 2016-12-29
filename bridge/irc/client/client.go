package bircc

import (
    "crypto/tls"
    "fmt"
    "github.com/42wim/matterbridge/bridge/irc"
    "github.com/42wim/matterbridge/bridge/config"
    log "github.com/Sirupsen/logrus"
    ircm "github.com/sorcix/irc"
    "github.com/thoj/go-ircevent"
    "regexp"
    "sort"
    "strconv"
    "strings"
    "time"
)

type Bircc struct {
    i         *irc.Connection
    Nick      string
    names     map[string][]string
    Config    *config.Protocol
    Remote    chan config.Message
    connected chan struct{}
    Local     chan config.Message // local queue for flood control
    Account   string
}

var flog *log.Entry
var protocol = "ircc"

func init() {
    flog = log.WithFields(log.Fields{"module": protocol})
}

func New(cfg config.Protocol, account string, c chan config.Message) *Bircc {
    b := &Bircc{}
    b.Config = &cfg
    b.Nick = b.Config.Nick
    b.Remote = c
    b.names = make(map[string][]string)
    b.Account = account
    b.connected = make(chan struct{})
    if b.Config.MessageDelay == 0 {
        b.Config.MessageDelay = 1300
    }
    if b.Config.MessageQueue == 0 {
        b.Config.MessageQueue = 30
    }
    b.Local = make(chan config.Message, b.Config.MessageQueue+10)
    return b
}

func (b *Bircc) Command(msg *config.Message) string {
    switch msg.Text {
    case "!users":
        b.i.AddCallback(ircm.RPL_NAMREPLY, b.storeNames)
        b.i.AddCallback(ircm.RPL_ENDOFNAMES, b.endNames)
        b.i.SendRaw("NAMES " + msg.Channel)
    }
    return ""
}

func (b *Bircc) Connect() error {
    flog.Infof("Connecting %s", b.Config.Server)
    i := irc.IRC(b.Config.Nick, b.Config.Nick)
    if log.GetLevel() == log.DebugLevel {
        i.Debug = true
    }
    i.UseTLS = b.Config.UseTLS
    i.UseSASL = b.Config.UseSASL
    i.SASLLogin = b.Config.NickServNick
    i.SASLPassword = b.Config.NickServPassword
    i.TLSConfig = &tls.Config{InsecureSkipVerify: b.Config.SkipTLSVerify}
    if b.Config.Password != "" {
        i.Password = b.Config.Password
    }
    i.AddCallback(ircm.RPL_WELCOME, b.handleNewConnection)
    err := i.Connect(b.Config.Server)
    if err != nil {
        return err
    }
    b.i = i
    select {
    case <-b.connected:
        flog.Info("Connection succeeded")
    case <-time.After(time.Second * 30):
        return fmt.Errorf("connection timed out")
    }
    i.Debug = false
    go b.doSend()
    return nil
}

func (b *Bircc) JoinChannel(channel string) error {
    b.i.Join(channel)
    return nil
}

func (b *Bircc) Send(msg config.Message) error {
    flog.Debugf("Receiving %#v", msg)
    if msg.Account == b.Account {
        return nil
    }
    if strings.HasPrefix(msg.Text, "!") {
        b.Command(&msg)
        return nil
    }
    text := strings.Replace(msg.Text, "\n", " ↵ ", -1)
    if len(strings.TrimSpace(text)) == 0 {
        return nil
    }
    if len(b.Local) < b.Config.MessageQueue {
        if len(b.Local) == b.Config.MessageQueue-1 {
            text = text + " <message clipped>"
        }
        b.Local <- config.Message{Text: text, Username: msg.Username, Channel: msg.Channel, Event: msg.Event}
    } else {
        flog.Debugf("flooding, dropping message (queue at %d)", len(b.Local))
    }
    return nil
}

func (b *Bircc) doSend() {
    rate := time.Millisecond * time.Duration(b.Config.MessageDelay)
    throttle := time.Tick(rate)
    for msg := range b.Local {
        <-throttle
        nick := birc.FormatNick(msg.Username)
        if msg.Event == config.EVENT_EDIT {
            nick += "/\x02Edit\x02"
        }
        b.i.Privmsg(msg.Channel, "["+nick+"] "+msg.Text)
    }
}

func (b *Bircc) endNames(event *irc.Event) {
    channel := event.Arguments[1]
    sort.Strings(b.names[channel])
    maxNamesPerPost := (300 / b.nicksPerRow()) * b.nicksPerRow()
    continued := false
    for len(b.names[channel]) > maxNamesPerPost {
        b.Remote <- config.Message{Username: b.Nick, Text: b.formatnicks(b.names[channel][0:maxNamesPerPost], continued),
            Channel: channel, Account: b.Account}
        b.names[channel] = b.names[channel][maxNamesPerPost:]
        continued = true
    }
    b.Remote <- config.Message{Username: b.Nick, Text: b.formatnicks(b.names[channel], continued),
        Channel: channel, Account: b.Account}
    b.names[channel] = nil
    b.i.ClearCallback(ircm.RPL_NAMREPLY)
    b.i.ClearCallback(ircm.RPL_ENDOFNAMES)
}

func (b *Bircc) handleNewConnection(event *irc.Event) {
    flog.Debug("Registering callbacks")
    i := b.i
    b.Nick = event.Arguments[0]
    i.AddCallback("PRIVMSG", b.handlePrivMsg)
    i.AddCallback("CTCP_ACTION", b.handlePrivMsg)
    i.AddCallback(ircm.RPL_TOPICWHOTIME, b.handleTopicWhoTime)
    i.AddCallback(ircm.NOTICE, b.handleNotice)
    //i.AddCallback(ircm.RPL_MYINFO, func(e *irc.Event) { flog.Infof("%s: %s", e.Code, strings.Join(e.Arguments[1:], " ")) })
    i.AddCallback("PING", func(e *irc.Event) {
        i.SendRaw("PONG :" + e.Message())
        flog.Debugf("PING/PONG")
    })
    i.AddCallback("JOIN", b.handleJoinPart)
    i.AddCallback("PART", b.handleJoinPart)
    i.AddCallback("QUIT", b.handleJoinPart)
    i.AddCallback("*", b.handleOther)
    // we are now fully connected
    b.connected <- struct{}{}
}

func (b *Bircc) handleJoinPart(event *irc.Event) {
    flog.Debugf("Sending JOIN_LEAVE event from %s to gateway", b.Account)
    channel := event.Arguments[0]
    if event.Code == "QUIT" {
        channel = ""
    }
    b.Remote <- config.Message{Username: "system", Text: event.Nick + " " + strings.ToLower(event.Code) + "s", Channel: channel, Account: b.Account, Event: config.EVENT_JOIN_LEAVE}
    flog.Debugf("handle %#v", event)
}

func (b *Bircc) handleNotice(event *irc.Event) {
    if strings.Contains(event.Message(), "This nickname is registered") && event.Nick == b.Config.NickServNick {
        b.i.Privmsg(b.Config.NickServNick, "IDENTIFY "+b.Config.NickServPassword)
    } else {
        event.Nick = "-"+event.Nick+"-"
        b.handlePrivMsg(event)
    }
}

func (b *Bircc) handleOther(event *irc.Event) {
    switch event.Code {
    case "372", "375", "376", "250", "251", "252", "253", "254", "255", "265", "266", "002", "003", "004", "005":
        return
    }
    flog.Debugf("%#v", event.Raw)
}

func (b *Bircc) handlePrivMsg(event *irc.Event) {
    // don't forward queries to the bot
    if event.Arguments[0] == b.Nick {
        return
    }
    // don't forward message from ourself
    if event.Nick == b.Nick {
        return
    }
    flog.Debugf("handlePrivMsg() %s %s %#v", event.Nick, event.Message(), event)
    msg := event.Message()
    if event.Code == "CTCP_ACTION" {
        msg = "*"+msg+"*"
    }
    // strip IRC colors
    re := regexp.MustCompile(`[[:cntrl:]](\d+,|)\d+`)
    msg = re.ReplaceAllString(msg, "")
    flog.Debugf("Sending message from %s on %s to gateway", event.Arguments[0], b.Account)
    b.Remote <- config.Message{Username: event.Nick, Text: msg, Channel: event.Arguments[0], Account: b.Account}
}

func (b *Bircc) handleTopicWhoTime(event *irc.Event) {
    parts := strings.Split(event.Arguments[2], "!")
    t, err := strconv.ParseInt(event.Arguments[3], 10, 64)
    if err != nil {
        flog.Errorf("Invalid time stamp: %s", event.Arguments[3])
    }
    user := parts[0]
    if len(parts) > 1 {
        user += " [" + parts[1] + "]"
    }
    flog.Debugf("%s: Topic set by %s [%s]", event.Code, user, time.Unix(t, 0))
}

func (b *Bircc) nicksPerRow() int {
    return 4
    /*
        if b.Config.Mattermost.NicksPerRow < 1 {
            return 4
        }
        return b.Config.Mattermost.NicksPerRow
    */
}

func (b *Bircc) storeNames(event *irc.Event) {
    channel := event.Arguments[2]
    b.names[channel] = append(
        b.names[channel],
        strings.Split(strings.TrimSpace(event.Message()), " ")...)
}

func (b *Bircc) formatnicks(nicks []string, continued bool) string {
    return birc.Plainformatter(nicks, b.nicksPerRow())
    /*
        switch b.Config.Mattermost.NickFormatter {
        case "table":
            return birc.Tableformatter(nicks, b.nicksPerRow(), continued)
        default:
            return birc.Plainformatter(nicks, b.nicksPerRow())
        }
    */
}
