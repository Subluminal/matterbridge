package bdiscord

import (
	"github.com/42wim/matterbridge/bridge/config"
	log "github.com/Sirupsen/logrus"
	"github.com/bwmarrin/discordgo"
	"strings"
    "regexp"
    "fmt"
)

type bdiscord struct {
	c            *discordgo.Session
	Config       *config.Protocol
	Remote       chan config.Message
	Account      string
	Channels     []*discordgo.Channel
	Nick         string
	UseChannelID bool
}

var flog *log.Entry
var protocol = "discord"
var mentionRegex = regexp.MustCompile("@\\w+")

func init() {
	flog = log.WithFields(log.Fields{"module": protocol})
}

func New(cfg config.Protocol, account string, c chan config.Message) *bdiscord {
	b := &bdiscord{}
	b.Config = &cfg
	b.Remote = c
	b.Account = account
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
            mention := "<@"+member.User.ID+">"
            nickMap[strings.ToLower(member.User.Username)] = mention
            if member.Nick != "" {
                nickMap[strings.ToLower(member.Nick)] = mention
            }
        }
    }
    text := mentionRegex.ReplaceAllStringFunc(msg.Text, func (match string) string {
        flog.Debugf("Searching for %s", match)
        nick := match[1:]
        if val, ok := nickMap[nick]; ok {
            flog.Debugf("Found %s", val)
            return val
        }
        return match
    })
	b.c.ChannelMessageSend(channelID, "<**"+msg.Username+"**> "+text)
	return nil
}

func (b *bdiscord) messageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	// not relay our own messages
	if m.Author.Username == b.Nick {
		return
	}
	if len(m.Attachments) > 0 {
		for pos, attach := range m.Attachments {
			m.Content = fmt.Sprintf("%s\n(%d/%d) [%s] %s", m.Content, pos, len(m.Attachments), attach.Filename, attach.URL)
		}
	}
	if m.Content == "" {
		return
	}
	flog.Debugf("Sending message from %s on %s to gateway", m.Author.Username, b.Account)
	channelName := b.getChannelName(m.ChannelID)
	if b.UseChannelID {
		channelName = "ID:" + m.ChannelID
	}
    nick := m.Author.Username
    for _, ch := range b.Channels {
        member, err := s.GuildMember(ch.GuildID, m.Author.ID)
        if err == nil && member.Nick != "" {
            nick = member.Nick
            break
        }
    }
	b.Remote <- config.Message{Username: nick, Text: m.ContentWithMentionsReplaced(), Channel: channelName,
		Account: b.Account, Avatar: "https://cdn.discordapp.com/avatars/" + m.Author.ID + "/" + m.Author.Avatar + ".jpg"}
}

func (b *bdiscord) messageUpdate(s *discordgo.Session, m *discordgo.MessageUpdate) {
    if m == nil {
        return
    }
	if len(m.Attachments) > 0 {
		for pos, attach := range m.Attachments {
			m.Content = fmt.Sprintf("%s\n(%d/??) [%s] %s", m.Content, pos, attach.Filename, attach.URL)
		}
	}
	flog.Debugf("Sending message from %s on %s to gateway", m.Author.Username, b.Account)
	channelName := b.getChannelName(m.ChannelID)
	if b.UseChannelID {
		channelName = "ID:" + m.ChannelID
	}
    nick := m.Author.Username
    for _, ch := range b.Channels {
        member, err := s.GuildMember(ch.GuildID, m.Author.ID)
        if err == nil && member.Nick != "" {
            nick = member.Nick
            break
        }
    }
    avatarUrl := "https://cdn.discordapp.com/avatars/" + m.Author.ID + "/" + m.Author.Avatar + ".jpg"
    b.Remote <- config.Message{Username: nick, Text: m.ContentWithMentionsReplaced(), Channel: channelName,
    Account: b.Account, Avatar: avatarUrl, Event: config.EVENT_EDIT}
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
