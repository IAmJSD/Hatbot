package main

import (
	"bytes"
	"context"
	"github.com/disintegration/imaging"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/andersfylling/disgord"
	"github.com/auttaja/gommand"
	"github.com/sirupsen/logrus"

	vision "cloud.google.com/go/vision/apiv1"
	pb "google.golang.org/genproto/googleapis/cloud/vision/v1"
)

var visionClient, _ = vision.NewImageAnnotatorClient(context.Background())

var propeller image.Image

var mongoClient *mongo.Client

// Initialise the propeller/mongodb client.
func init() {
	f, err := os.Open("propeller.png")
	if err != nil {
		panic(err)
	}
	propeller, err = png.Decode(f)
	if err != nil {
		panic(err)
	}
	err = f.Close()
	if err != nil {
		panic(err)
	}
	URI := os.Getenv("MONGO_URI")
	if URI == "" {
		URI = "mongodb://localhost:27017"
	}
	mongoClient, err = mongo.Connect(context.TODO(), options.Client().ApplyURI(URI))
	if err != nil {
		panic(err)
	}
	err = mongoClient.Ping(context.TODO(), readpref.Primary())
	if err != nil {
		panic(err)
	}
}

// HatData is used to define the data used for a hat.
type HatData struct {
	UserID    string `bson:"_id"`
	CustomHat []byte `bson:"customHat"`
}

// Get the image to use as the hat.
func getHat(uid disgord.Snowflake) image.Image {
	var data HatData
	err := mongoClient.Database("hatbot").Collection("hats").FindOne(context.TODO(), bson.M{"_id": uid.String()}).Decode(&data)
	if err == mongo.ErrNoDocuments {
		return propeller
	} else if err == nil {
		img, _ := png.Decode(bytes.NewReader(data.CustomHat))
		return img
	} else {
		panic(err)
	}
}

// Detect if a channel is allowed.
func channelAllowed(channelId disgord.Snowflake) bool {
	err := mongoClient.Database("hatbot").Collection("channels").FindOne(context.TODO(), bson.M{"_id": channelId.String()}).Err()
	return err != mongo.ErrNoDocuments
}

// Create the command router.
var router = gommand.NewRouter(&gommand.RouterConfig{
	PrefixCheck: gommand.MentionPrefix,
})

// Handles a new message.
func messageCreate(s disgord.Session, evt *disgord.MessageCreate) {
	if evt.Message.Author.Bot || len(evt.Message.Attachments) == 0 {
		// ignore this - is a bot or no attachments
		return
	}
	go func() {
		// Check if the channel is allowed.
		if !channelAllowed(evt.Message.ChannelID) {
			return
		}

		// Check for any images.
		images := make([]*disgord.Attachment, 0, len(evt.Message.Attachments))
		exts := []string{"png", "jpg", "jpeg"}
		for _, v := range evt.Message.Attachments {
			filenameLower := strings.ToLower(v.Filename)
			for _, ext := range exts {
				if strings.HasSuffix(filenameLower, ext) {
					images = append(images, v)
					break
				}
			}
		}
		if len(images) == 0 {
			return
		}

		// Defines the images with hats on them.
		hatified := make([][]byte, 0, len(images))

		// Check if we need to put a hat on each image.
		for _, imgMetadata := range images {
			// Get the vision image reader.
			resp, err := http.Get(imgMetadata.URL)
			if err != nil {
				// Discord fucked up here.
				logrus.Error("Discord image get fail:", err)
				return
			}
			img, err := vision.NewImageFromReader(resp.Body)
			if err != nil {
				// Hmmm weird.
				logrus.Error(err)
				return
			}
			resp.Body.Close()

			// Get all animal crops in the image.
			crops, err := visionClient.LocalizeObjects(context.TODO(), img, nil)
			if err != nil {
				// Hmmm weird.
				logrus.Error(err)
				return
			}
			animals := make([]*pb.LocalizedObjectAnnotation, 0, len(crops))
			for _, v := range crops {
				if v.Name == "Dog" || v.Name == "Cat" || v.Name == "Parrot" || v.Name == "Hamster" || v.Name == "Duck" || v.Name == "Animal" {
					animals = append(animals, v)
				}
			}
			if len(animals) == 0 {
				continue
			}

			// Decode the image locally.
			var imgObjUncasted image.Image
			if strings.HasSuffix(strings.ToLower(imgMetadata.URL), "png") {
				imgObjUncasted, err = png.Decode(bytes.NewReader(img.Content))
			} else {
				imgObjUncasted, err = jpeg.Decode(bytes.NewReader(img.Content))
			}
			if err != nil {
				logrus.Error(err)
				return
			}
			imgObj, ok := imgObjUncasted.(draw.Image)
			if !ok {
				imgObj = image.NewRGBA(imgObjUncasted.Bounds())
				draw.Draw(imgObj, imgObj.Bounds(), imgObjUncasted, image.Point{}, draw.Src)
			}

			// Create the rectangle for each animal.
			ImageX := imgObj.Bounds().Dx()
			ImageY := imgObj.Bounds().Dy()
			rects := make([]image.Rectangle, len(animals))
			for i, animal := range animals {
				LowestX := 9999999999
				LowestY := 9999999999
				HighestX := 0
				HighestY := 0
				for _, verts := range animal.BoundingPoly.NormalizedVertices {
					RealY := int(verts.Y * float32(ImageY))
					RealX := int(verts.X * float32(ImageX))
					if LowestX > RealX {
						LowestX = RealX
					}
					if LowestY > RealY {
						LowestY = RealY
					}
					if RealX > HighestX {
						HighestX = RealX
					}
					if RealY > HighestY {
						HighestY = RealY
					}
				}
				rects[i] = image.Rect(
					LowestX,
					LowestY,
					HighestX,
					HighestY,
				)
			}

			// Draw on the animals.
			for _, rect := range rects {
				// Guess the X length of the head.
				var HeadX int
				TotalX := rect.Max.X - rect.Min.X
				TotalY := rect.Max.Y - rect.Min.Y
				divNum := 6
				if TotalY > TotalX {
					// This is probably a animal sitting up.
					HeadX = TotalX
				} else {
					// Guess the dogs head length.
					HeadX = TotalX - int(float64(TotalX)/2.5) - (TotalX / 20)
					divNum = 4
				}

				// Resize the hat.
				hatResize := imaging.Resize(getHat(evt.Message.Author.ID), HeadX, 0, imaging.Lanczos)

				// Get the start point.
				HatPoint := image.Point{
					X: rect.Max.X - hatResize.Bounds().Dx(),
					Y: rect.Min.Y - (hatResize.Bounds().Dy() - (hatResize.Bounds().Dy() / divNum)),
				}
				draw.Draw(imgObj, hatResize.Bounds().Add(HatPoint), hatResize, image.Point{}, draw.Over)
			}

			// Encode as a PNG.
			buf := &bytes.Buffer{}
			err = png.Encode(buf, imgObj)
			if err != nil {
				panic(err)
			}
			hatified = append(hatified, buf.Bytes())
		}

		// If the length of hatified isn't 0, send the pets.
		if len(hatified) != 0 {
			data := make([]interface{}, len(hatified)+2)
			data[0] = evt.Message.Author.Mention()
			data[1] = "I put hats on those animals for you 🙂"
			for i, img := range hatified {
				data[i+2] = &disgord.CreateMessageFileParams{
					Reader:     bytes.NewReader(img),
					FileName:   "hats.png",
					SpoilerTag: false,
				}
			}
			_, _ = s.SendMsg(context.TODO(), evt.Message.ChannelID, data...)
		}
	}()
}

func main() {
	// Handles command errors where possible. If not, just passes it through to the default handler to log to console.
	router.AddErrorHandler(func(ctx *gommand.Context, err error) bool {
		// Check all the different types of errors.
		switch err.(type) {
		case *gommand.CommandNotFound, *gommand.CommandBlank:
			// We will ignore.
			return true
		case *gommand.InvalidTransformation:
			_, _ = ctx.Reply("Invalid argument:", err.Error())
			return true
		case *gommand.IncorrectPermissions:
			_, _ = ctx.Reply("Invalid permissions:", err.Error())
			return true
		case *gommand.InvalidArgCount:
			_, _ = ctx.Reply("Invalid argument count.")
			return true
		}

		// This was not handled here.
		return false
	})

	// Register all commands.
	router.RemoveCommand(router.GetCommand("help"))
	router.SetCommand(help)
	router.SetCommand(setHat)
	router.SetCommand(rmHat)
	router.SetCommand(toggleChannel)

	// Initialises the client.
	logrus.SetLevel(logrus.DebugLevel)
	client := disgord.New(disgord.Config{
		BotToken: os.Getenv("TOKEN"),
		Logger:   logrus.New(),
		CacheConfig: &disgord.CacheConfig{
			DisableChannelCaching:    true,
			DisableGuildCaching:      true,
			DisableUserCaching:       true,
			DisableVoiceStateCaching: true,
		},
	})
	router.Hook(client)
	client.On(disgord.EvtReady, func(s disgord.Session, evt *disgord.Ready) {
		go func() {
			for {
				_ = s.UpdateStatusString("@" + evt.User.Tag() + " " + "help | 🐶 🐱")
				time.Sleep(time.Second * 20)
			}
		}()
	})
	client.On(disgord.EvtMessageCreate, messageCreate)
	_ = client.StayConnectedUntilInterrupted(context.Background())
}
