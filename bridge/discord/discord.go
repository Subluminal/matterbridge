package bdiscord

import (
    "github.com/42wim/matterbridge/bridge/config"
    log "github.com/Sirupsen/logrus"
    "github.com/bwmarrin/discordgo"
    "strings"
    "regexp"
    "fmt"
)

type Attachment struct {
    id           string
    form         string
}

type Message struct {
    id           string
    content      string
    attachments  []Attachment
}

type bdiscord struct {
    c            *discordgo.Session
    Config       *config.Protocol
    Remote       chan config.Message
    Account      string
    Channels     []*discordgo.Channel
    Nick         string
    UseChannelID bool
    MessageHist  []Message
}

var flog *log.Entry
var protocol = "discord"
var mentionRegex = regexp.MustCompile(`<@!?(\d+)>`)
var chanRegex = regexp.MustCompile(`<#(\d+)>`)
var emojiRegex = regexp.MustCompile(`<(:\w+:)\d+>`)
var pingRegex = regexp.MustCompile(`@\w+`)

func init() {
    flog = log.WithFields(log.Fields{"module": protocol})
}

func New(cfg config.Protocol, account string, c chan config.Message) *bdiscord {
    b := &bdiscord{}
    b.Config = &cfg
    b.Remote = c
    b.Account = account
    b.MessageHist = make([]Message, 64)
    return b
}

func (b *bdiscord) Connect() error {
    var err error
    flog.Info("Connecting")
    if !strings.HasPrefix(b.Config.Token, "Bot ") {
        b.Config.Token = "Bot " + b.Config.Token
    }
    b.c, err = discordgo.New(b.Config.Token)
    if err != nil {
        flog.Debugf("%#v", err)
        return err
    }
    flog.Info("Connection succeeded")
    b.c.AddHandler(b.messageCreate)
    b.c.AddHandler(b.messageUpdate)
    err = b.c.Open()
    if err != nil {
        flog.Debugf("%#v", err)
        return err
    }
    guilds, err := b.c.UserGuilds()
    if err != nil {
        flog.Debugf("%#v", err)
        return err
    }
    userinfo, err := b.c.User("@me")
    if err != nil {
        flog.Debugf("%#v", err)
        return err
    }
    b.Nick = userinfo.Username
    for _, guild := range guilds {
        if guild.Name == b.Config.Server {
            b.Channels, err = b.c.GuildChannels(guild.ID)
            if err != nil {
                flog.Debugf("%#v", err)
                return err
            }
        }
    }
    return nil
}

func (b *bdiscord) JoinChannel(channel string) error {
    idcheck := strings.Split(channel, "ID:")
    if len(idcheck) > 1 {
        b.UseChannelID = true
    }
    return nil
}

func (b *bdiscord) Send(msg config.Message) error {
    flog.Debugf("Receiving %#v", msg)
    channelID := b.getChannelID(msg.Channel)
    if channelID == "" {
        flog.Errorf("Could not find channelID for %v", msg.Channel)
        return nil
    }
    guilds := map[string]struct{}{}
    for _, ch := range b.Channels {
        guilds[ch.GuildID] = struct{}{}
    }
    nickMap := map[string]string{}
    for guildid := range guilds {
        members, err := b.c.GuildMembers(guildid, 0, 1000)
        if err != nil {
            continue
        }
        for _, member := range members {
            hl := "<@"+member.User.ID+">"
            nickMap[strings.ToLower(member.User.Username)] = hl
            if member.Nick != "" {
                nickMap[strings.ToLower(member.Nick)] = hl
            }
        }
    }
    text := pingRegex.ReplaceAllStringFunc(msg.Text, func (match string) string {
        flog.Debugf("Searching for %s", match)
        nick := strings.ToLower(match[1:])
        if val, ok := nickMap[nick]; ok {
            flog.Debugf("Found %s", val)
            return val
        }
        return match
    })
    b.c.ChannelMessageSend(channelID, "<**"+msg.Username+"**> "+text)
    return nil
}

func (b *bdiscord) findNick(s *discordgo.Session, user *discordgo.User) string {
    name := user.Username
    for _, ch := range b.Channels {
        member, err := s.GuildMember(ch.GuildID, user.ID)
        if err == nil && member.Nick != "" {
            name = member.Nick
            break
        }
    }
    return "@"+name
}

func (b *bdiscord) getAvatar(user *discordgo.User) string {
    return "https://cdn.discordapp.com/avatars/" + user.ID + "/" + user.Avatar + ".jpg"
}

func (b *bdiscord) CleanContent(content string) string {
    guilds := map[string]struct{}{}
    for _, ch := range b.Channels {
        guilds[ch.GuildID] = struct{}{}
    }
    nickMap := map[string]string{}
    chanMap := map[string]string{}
    for guildid := range guilds {
        channels, cerr := b.c.GuildChannels(guildid)
        if cerr == nil {
            for _, channel := range channels {
                chanMap[channel.ID] = "#"+channel.Name
            }
        }
        members, merr := b.c.GuildMembers(guildid, 0, 1000)
        if merr == nil {
            for _, member := range members {
                nickMap[member.User.ID] = "@"+member.User.Username
                if member.Nick != "" {
                    nickMap[member.User.ID] = "@"+member.Nick
                }
            }
        }
    }
    text := chanRegex.ReplaceAllStringFunc(content, func (match string) string {
        id := chanRegex.FindStringSubmatch(match)[1]
        flog.Debugf("Searching for %s", id)
        if val, ok := chanMap[id]; ok {
            flog.Debugf("Found %s", val)
            return val
        }
        return match
    })
    text = mentionRegex.ReplaceAllStringFunc(text, func (match string) string {
        id := mentionRegex.FindStringSubmatch(match)[1]
        flog.Debugf("Searching for %s", id)
        if val, ok := nickMap[id]; ok {
            flog.Debugf("Found %s", val)
            return val
        }
        return match
    })
    return emojiRegex.ReplaceAllStringFunc(text, func (match string) string {
        return emojiRegex.FindStringSubmatch(match)[1]
    })
}

func (b *bdiscord) messageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
    // not relay our own messages
    if m.Author.Username == b.Nick {
        return
    }
    log.Infof("! %#v", m.Content)
    text := b.CleanContent(m.Content)
    attachments := []Attachment{}
    rest := ""
    if len(m.Attachments) > 0 {
        for pos, attach := range m.Attachments {
            form := "[" + attach.Filename + "] " + attach.URL
            attachments = append(attachments, Attachment{attach.ID, form})
            rest = fmt.Sprintf("%s\n(%d/%d) %s", rest, pos+1, len(m.Attachments), form)
        }
    }
    b.MessageHist = append(b.MessageHist[1:], Message{m.ID, text, attachments})
    if text == "" && rest == "" {
        return
    }
    flog.Debugf("Sending message from %s on %s to gateway", m.Author.Username, b.Account)
    channelName := b.getChannelName(m.ChannelID)
    if b.UseChannelID {
        channelName = "ID:" + m.ChannelID
    }
    b.Remote <- config.Message{Username: b.findNick(s, m.Author), Text: text+rest, Channel: channelName,
        Account: b.Account, Avatar: b.getAvatar(m.Author)}
}

func (b *bdiscord) messageUpdate(s *discordgo.Session, m *discordgo.MessageUpdate) {
    if m == nil {
        return
    }
    var o *Message = nil
    for _, v := range b.MessageHist {
        if v.id == m.ID {
            o = &v
        }
    }
    text := b.CleanContent(m.Content)
    attachments := []Attachment{}
    rest := ""
    if len(m.Attachments) > 0 {
        Loop: for pos, attach := range m.Attachments {
            if o != nil {
                for _, v := range o.attachments {
                    if v.id == attach.ID {
                        continue Loop
                    }
                }
            }
            form := "[" + attach.Filename + "] " + attach.URL
            attachments = append(attachments, Attachment{attach.ID, form})
            rest = fmt.Sprintf("%s\n(%d/??) %s", rest, pos+1, form)
        }
    }
    b.MessageHist = append(b.MessageHist[1:], Message{m.ID, text, attachments})
    if o != nil && len(o.content)+len(text) < 420 {
        text = o.content + " -> " + text
    } else {
        text = "[???] -> " + text
    }
    if m.Author == nil {
        flog.Debugf("ignoring edit %#v on %s", m.Message, b.Account)
        return
    }
    flog.Debugf("Sending edit from %s on %s to gateway", m.Author.Username, b.Account)
    channelName := b.getChannelName(m.ChannelID)
    if b.UseChannelID {
        channelName = "ID:" + m.ChannelID
    }
    b.Remote <- config.Message{Username: b.findNick(s, m.Author), Text: text+rest, Channel: channelName,
        Account: b.Account, Avatar: b.getAvatar(m.Author), Event: config.EVENT_EDIT}
}

func (b *bdiscord) getChannelID(name string) string {
    idcheck := strings.Split(name, "ID:")
    if len(idcheck) > 1 {
        return idcheck[1]
    }
    for _, channel := range b.Channels {
        if channel.Name == name {
            return channel.ID
        }
    }
    return ""
}

func (b *bdiscord) getChannelName(id string) string {
    for _, channel := range b.Channels {
        if channel.ID == id {
            return channel.Name
        }
    }
    return ""
}
