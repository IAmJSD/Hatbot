package main

import (
	"bytes"
	"context"
	"github.com/auttaja/gommand"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
	"image/png"
	"io/ioutil"
	"net/http"
)

var setHat = &gommand.Command{
	Name:        "sethat",
	Description: "Allows you to set a hat.",
	Usage:       "<attached png image>",
	Function: func(ctx *gommand.Context) error {
		// Error if no attachment found.
		if len(ctx.Message.Attachments) == 0 {
			_, _ = ctx.Reply("A PNG image is required.")
			return nil
		}

		// Get the bytes.
		resp, err := http.Get(ctx.Message.Attachments[0].URL)
		if err != nil {
			// Discord fucked up here.
			_, _ = ctx.Reply("Discord error:", err.Error())
			return nil
		}
		defer resp.Body.Close()
		b, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			// Discord fucked up here.
			_, _ = ctx.Reply("Discord error:", err.Error())
			return nil
		}

		// Sanity check: Try decoding the PNG.
		_, err = png.Decode(bytes.NewReader(b))
		if err != nil {
			// Discord fucked up here.
			_, _ = ctx.Reply("Invalid PNG:", err.Error())
			return nil
		}

		// Save the bytes in MongoDB.
		_, err = mongoClient.Database("hatbot").Collection("hats").UpdateOne(context.TODO(), bson.M{"_id": ctx.Message.Author.ID.String()}, bson.M{"$set": &HatData{
			UserID:    ctx.Message.Author.ID.String(),
			CustomHat: b,
		}}, options.Update().SetUpsert(true))
		if err != nil {
			// Discord fucked up here.
			_, _ = ctx.Reply("Mongo insert error:", err.Error())
			return nil
		}

		// Return no errors.
		_, _ = ctx.Reply("Custom hat added.")
		return nil
	},
}

var rmHat = &gommand.Command{
	Name:        "rmhat",
	Description: "Allows you to remove a hat if you set a custom one.",
	Function: func(ctx *gommand.Context) error {
		res, err := mongoClient.Database("hatbot").Collection("hats").DeleteOne(context.TODO(), bson.M{"_id": ctx.Message.Author.ID.String()})
		if err != nil {
			_, _ = ctx.Reply("Mongo delete error:", err.Error())
		} else if res.DeletedCount == 0 {
			_, _ = ctx.Reply("No custom hat set.")
		} else {
			_, _ = ctx.Reply("Custom hat deleted.")
		}
		return nil
	},
}

var toggleChannel = &gommand.Command{
	Name:                 "togglechannel",
	Description:          "Toggle if the channel you're in will have images processed. By default, I don't process channels, this has to be toggled by running this command.",
	PermissionValidators: []gommand.PermissionValidator{gommand.MANAGE_MESSAGES(gommand.CheckMembersChannelPermissions)},
	Function: func(ctx *gommand.Context) error {
		res, err := mongoClient.Database("hatbot").Collection("channels").DeleteOne(context.TODO(), bson.M{"_id": ctx.Message.ChannelID.String()})
		onOff := "off"
		if err == nil && res.DeletedCount == 0 {
			_, err = mongoClient.Database("hatbot").Collection("channels").InsertOne(context.TODO(), bson.M{"_id": ctx.Message.ChannelID.String()})
			onOff = "on"
		}
		if err != nil {
			_, _ = ctx.Reply("Mongo error:", err.Error())
			return nil
		}
		_, _ = ctx.Reply("Channel toggled", onOff)
		return nil
	},
}

var help = &gommand.Command{
	Name:        "help",
	Description: "Get help for the bot.",
	Function: func(ctx *gommand.Context) error {
		allCmds := ctx.Router.GetAllCommands()
		helpstr := "**Welcome to HatBot**\nThe ultimate bot to put hats on pets.\n\n"
		for _, v := range allCmds {
			if gommand.CommandHasPermission(ctx, v) == nil {
				helpstr += ctx.BotUser.Mention() + " " + v.GetName() + " - " + v.GetDescription() + "\n"
			}
		}
		_, _ = ctx.Reply(helpstr)
		return nil
	},
}

var invite = &gommand.Command{
	Name:        "invite",
	Description: "Returns a invite for the bot.",
	Function: func(ctx *gommand.Context) error {
		_, _ = ctx.Reply("You can invite the bot with this URL: https://discord.com/oauth2/authorize?client_id="+ctx.BotUser.ID.String()+"&scope=bot&permissions=52224")
		return nil
	},
}
