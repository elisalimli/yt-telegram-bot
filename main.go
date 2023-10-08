package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/gotd/td/examples"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/message"
	"github.com/gotd/td/telegram/message/html"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"

	amqp "github.com/rabbitmq/amqp091-go"
)

func getVideoTitle(youtubeUrl string) string {
	// Define the command to run
	cmd := exec.Command("yt-dlp", "--print", "%(title)s", youtubeUrl)

	// Run the command and capture its output
	output, err := cmd.Output()
	if err != nil {
		fmt.Println("Error executing command:", err)
	}

	// Convert the output to a string and remove any leading/trailing whitespace
	title := strings.TrimSpace(string(output))

	// Print the YouTube video title
	fmt.Println("YouTube Video Title:", title)
	return title
}

func getAudioBytes(youtubeUrl string) ([]byte, error) {
	fmt.Println("Starting download...")

	// _, w := io.Pipe()
	// r2, w2 := io.Pipe()
	// // defer r.Close()
	// // defer w.Close()

	// ytdlp := exec.Command("yt-dlp", "-o-", youtubeUrl)
	// ytdlp.Stdout = w
	// ytdlp.Stderr = os.Stderr
	// ffmpeg := exec.Command("ffmpeg", "-i", "/dev/stdin", "-f", "mp3", "-ab", "96000", "-vn", "-")
	// ffmpeg.Stdin = r
	// ffmpeg.Stdout = w2
	// return r2
	var audioOutput bytes.Buffer

	// ytdlp := exec.Command("youtube-dl", "-x", "-o-", "--audio-format", "mp3", youtubeUrl)
	ytdlp := exec.Command("yt-dlp", "-f", "bestaudio", "-o-", "--audio-format", "mp3", youtubeUrl)
	// Create a multi-writer that writes to both audioOutput and os.Stdout
	ytdlp.Stdout = &audioOutput // Write to both terminal and buffer

	ytdlp.Stderr = os.Stderr

	if err := ytdlp.Run(); err != nil {
		return nil, err
	}

	return audioOutput.Bytes(), nil

}

func FailOnError(err error, msg string) {
	if err != nil {
		log.Panicf("%s: %s", msg, err)
	}
}

// CustomProgressTracker is a custom implementation of the Progress interface.
type CustomProgressTracker struct {
}

// Chunk is the implementation of the Chunk method of the Progress interface.
func (c *CustomProgressTracker) Chunk(ctx context.Context, state uploader.ProgressState) error {
	// Implement your progress tracking logic here.
	fmt.Printf("Upload Progress - ID: %d, Name: %s, Part: %d, PartSize: %d, Uploaded: %d, Total: %d\n",
		state.ID, state.Name, state.Part, state.PartSize, state.Uploaded, state.Total)
	// You can perform actions based on the progress state received.
	return nil
}

type Message struct {
	YoutubeURL string `json:"youtubeURL"`
	UserID     int64  `json:"userID"`
}

func main() {
	// Environment variables:
	//	BOT_TOKEN:     token from BotFather
	// 	APP_ID:        app_id of Telegram app.
	// 	APP_HASH:      app_hash of Telegram app.
	// 	SESSION_FILE:  path to session file
	// 	SESSION_DIR:   path to session directory, if SESSION_FILE is not set

	// conn, err := amqp.Dial("amqp://guest:guest@localhost:5672/")
	conn, err := amqp.Dial("amqp://guest:guest@rabbitmq:5672/")
	for err != nil {
		log.Println("Failed to connect to RabbitMQ")
		// FailOnError(err, "Failed to connect to RabbitMQ")
		log.Println("waiting 10 seconds")
		time.Sleep(time.Second * 10)
		// conn, err = amqp.Dial("amqp://guest:guest@localhost:5672/")

		conn, err = amqp.Dial("amqp://guest:guest@rabbitmq:5672/")
	}
	defer conn.Close()

	ch, err := conn.Channel()
	FailOnError(err, "Failed to open a channel")
	defer ch.Close()

	q, err := ch.QueueDeclare(
		"hello", // name
		false,   // durable
		false,   // delete when unused
		false,   // exclusive
		false,   // no-wait
		nil,     // arguments
	)
	FailOnError(err, "Failed to declare a queue")
	msgs, err := ch.Consume(
		q.Name, // queue
		"",     // consumer
		true,   // auto-ack
		false,  // exclusive
		false,  // no-local
		false,  // no-wait
		nil,    // args
	)
	FailOnError(err, "Failed to register a consumer")

	examples.Run(func(ctx context.Context, zapLogger *zap.Logger) error {
		// Dispatcher handles incoming updates.
		dispatcher := tg.NewUpdateDispatcher()
		opts := telegram.Options{
			// Logger:        zapLogger,
			UpdateHandler: dispatcher,
		}

		return telegram.BotFromEnvironment(ctx, opts, func(ctx context.Context, client *telegram.Client) error {
			// Raw MTProto API client, allows making raw RPC calls.
			api := tg.NewClient(client)

			progress := CustomProgressTracker{}

			// Helper for uploading. Automatically uses big file upload when needed.
			uploader := uploader.NewUploader(api).WithProgress(&progress).WithPartSize(524288)

			// Helper for sending messages.
			sender := message.NewSender(api)

			go func() {
				for d := range msgs {
					var msg Message

					err := json.Unmarshal(d.Body, &msg)
					if err != nil {
						log.Fatalf("Failed to serialize message: %v", err)
					}

					fmt.Printf("Message Received %v", msg)
					youtubeUrl := msg.YoutubeURL
					sender.To(&tg.InputPeerUser{UserID: msg.UserID}).Text(ctx, "message from kafka "+youtubeUrl)

					audioBytes, err := getAudioBytes(youtubeUrl)
					if err != nil {
						log.Printf("err %v", err)
						return
					}
					videoTitle := getVideoTitle(youtubeUrl)

					fmt.Println("Uploading file to telegram...")

					upload, err := uploader.FromBytes(ctx, videoTitle, audioBytes)
					if err != nil {
						fmt.Printf("upload error %v\n", err)
					}

					fmt.Println("Preparing message...")
					// Now we have uploaded file handle, sending it as styled message.
					// First, preparing message.
					document := message.UploadedDocument(upload,
						html.String(nil, `Upload: <b>From bot</b>`),
					)

					fmt.Println("Setting filename...")
					// You can set MIME type, send file as video or audio by using
					// document builder:
					document.
						MIME("audio/mp3").
						Filename(videoTitle).
						Audio()

					// Resolving target. Can be telephone number or @nickname of user,
					// group or channel.
					target := sender.To(&tg.InputPeerUser{UserID: msg.UserID})

					fmt.Println("Sending message...")
					// Sending message with media.
					// log.Info("Sending file")
					if _, err := target.Media(ctx, document); err != nil {
						fmt.Printf("err %v", err)
						// return fmt.Errorf("send: %w", err)
					}

				}
			}()

			log.Printf(" [*] Waiting for messages. To exit press CTRL+C")

			// Setting up handler for incoming message.
			dispatcher.OnNewMessage(func(ctx context.Context, entities tg.Entities, u *tg.UpdateNewMessage) error {
				m, ok := u.Message.(*tg.Message)
				if !ok || m.Out {
					// Outgoing message, not interesting.
					return nil
				}

				// m.PeerID.Encode(&bin.Buffer{})
				userID := int64(0)

				for entityUserID := range entities.Users {
					userID = entityUserID
					break
				}

				sender.To(&tg.InputPeerUser{UserID: int64(userID)}).Text(ctx, strconv.Itoa(int(userID)))
				youtubeUrl := m.Message
				if strings.Contains(youtubeUrl, "youtube.com") || strings.Contains(youtubeUrl, "youtu.be") {
					videoTitle := getVideoTitle(youtubeUrl)
					_, err := sender.Reply(entities, u).Text(ctx, videoTitle)
					if err != nil {
						return err
					}

					message := Message{
						YoutubeURL: youtubeUrl,
						UserID:     userID,
						// Set other fields as needed
					}

					// Serialize the message to JSON
					messageBody, err := json.Marshal(message)
					if err != nil {
						log.Fatalf("Failed to serialize message: %v", err)
					}

					err = ch.PublishWithContext(ctx,
						"",     // exchange
						q.Name, // routing key
						false,  // mandatory
						false,  // immediate
						amqp.Publishing{
							ContentType: "application/json",
							Body:        messageBody,
						})
					FailOnError(err, "Failed to publish a message")
					log.Printf(" [x] Sent %s\n", messageBody)

				} else {

					// Sending reply.
					_, err := sender.Reply(entities, u).Text(ctx, "❌ Invalid youtube video URL")
					return err
				}

				return nil
			})

			return nil
		}, telegram.RunUntilCanceled)

	})

}
