package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	pb "example.com/service"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// Store the status of all friends
var friendStatusMap sync.Map

// Store the list of friend IDs
var friendList []string
var friendListLock sync.Mutex

// Persistent stream for FriendListener
var stream pb.Server_FriendListenerClient
var streamLock sync.Mutex

func main() {
	var clientID string
	var initialFriends []string

	// Handle command line arguments for clientID and friends list
	if len(os.Args) > 1 {
		clientID = os.Args[1]
		if len(os.Args) > 2 {
			initialFriends = os.Args[2:] // All arguments after clientID are treated as friend IDs
		} else {
			log.Println("No friends provided.")
		}
	} else {
		clientID = uuid.New().String()
		log.Printf("No clientID provided. Generated UUID: %s", clientID)
		log.Println("No friends provided.")
	}

	log.Printf("Client ID: %s", clientID)
	log.Printf("Initial Friend list: %v", initialFriends)

	// Store the initial friend list
	friendListLock.Lock()
	friendList = initialFriends
	friendListLock.Unlock()

	// Reconnection logic for Ping/Pong Client
	go func() {
		for {
			err := runPingPongClient(clientID)
			if err != nil {
				log.Printf("PingPong connection lost: %v. Reconnecting...", err)
				time.Sleep(2 * time.Second) // Wait before reconnecting
			}
		}
	}()

	// Start the friend listener client
	go runFriendListenerClient(clientID, initialFriends)

	// Start the input listener for add/remove friend commands
	go listenForUserInput()

	select {}
}

// runPingPongClient handles the ping/pong logic
func runPingPongClient(clientID string) error {
	log.Printf("Starting PingPong client for ClientID: %s", clientID)

	// conn, err := grpc.Dial("localhost:50051", grpc.WithInsecure())
	conn, err := dialServer()
	if err != nil {
		log.Printf("Failed to connect to gRPC server: %v", err)
		return err
	}
	defer conn.Close()

	client := pb.NewServerClient(conn)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream, err := client.Communicate(ctx)
	if err != nil {
		return err
	}

	// Send ClientHello message
	err = stream.Send(&pb.ClientMessage{
		Message: &pb.ClientMessage_ClientHello{
			ClientHello: &pb.ClientHello{ClientId: clientID},
		},
	})
	if err != nil {
		return err
	}

	// Goroutine to handle ping/pong messages
	go func() {
		for {
			_, err := stream.Recv()
			if err != nil {
				log.Printf("Error receiving ping: %v", err)
				cancel() // Cancel the context, forcing a reconnection
				return
			}

			err = stream.Send(&pb.ClientMessage{
				Message: &pb.ClientMessage_Pong{
					Pong: &pb.Pong{Status: pb.Pong_FOREGROUND}, // Example: Always return FOREGROUND for now
				},
			})
			if err != nil {
				log.Printf("Error sending pong: %v", err)
				cancel() // Cancel the context, forcing a reconnection
				return
			}
		}
	}()

	<-ctx.Done() // Wait until context is canceled
	return ctx.Err()
}

// runFriendListenerClient listens for friend status updates
func runFriendListenerClient(clientID string, initialFriends []string) {
	log.Printf("🚀 Starting Friend Listener client for ClientID: %s with friends: %v", clientID, initialFriends)

	for {
		conn, err := dialServer()
		if err != nil {
			log.Printf("Failed to connect to gRPC server: %v", err)
			time.Sleep(2 * time.Second)
			continue
		}

		client := pb.NewServerClient(conn)
		ctx, cancel := context.WithCancel(context.Background())

		streamLock.Lock()
		stream, err = client.FriendListener(ctx)
		streamLock.Unlock()

		if err != nil {
			log.Printf("Failed to open stream: %v", err)
			cancel()
			time.Sleep(2 * time.Second)
			continue
		}

		// Send the initial friend list as an AddFriendList request
		err = stream.Send(&pb.FriendListenerRequest{
			Message: &pb.FriendListenerRequest_AddFriendList{
				AddFriendList: &pb.AddFriendList{FriendIds: initialFriends},
			},
		})
		if err != nil {
			log.Printf("❌ Failed to send AddFriendList: %v", err)
			cancel()
			time.Sleep(2 * time.Second)
			continue
		}
		log.Println("✅ Sent initial AddFriendList successfully")

		// Listen for server responses
		go func() {
			for {
				response, err := stream.Recv()
				if err != nil {
					log.Printf("Error receiving FriendListenerResponse: %v", err)
					cancel()
					return
				}

				switch msg := response.Message.(type) {
				case *pb.FriendListenerResponse_FriendUpdate:
					friendUpdate := msg.FriendUpdate
					log.Printf("📢 Friend update - ID: %s, Status: %v", friendUpdate.ClientId, friendUpdate.Status.String())
					updateFriendStatus(friendUpdate.ClientId, friendUpdate.Status)
				}
			}
		}()

		<-ctx.Done()
		time.Sleep(2 * time.Second)
	}
}

func updateFriendStatus(friendID string, status pb.FriendUpdate_Status) {
	var statusString string
	switch status {
	case pb.FriendUpdate_FOREGROUND:
		statusString = "FOREGROUND"
	case pb.FriendUpdate_BACKGROUND:
		statusString = "BACKGROUND"
	case pb.FriendUpdate_OFFLINE:
		statusString = "OFFLINE"
	default:
		statusString = "UNKNOWN"
	}

	friendStatusMap.Store(friendID, statusString)
	log.Printf("Updated friend %s to %s", friendID, statusString)
	printFriendStatusTable()
}

func printFriendStatusTable() {
	fmt.Println("====== Friend Status Table ======")
	friendStatusMap.Range(func(key, value interface{}) bool {
		clientID := key.(string)
		status := value.(string)
		fmt.Printf("Friend ID: %s | Status: %s\n", clientID, status)
		return true
	})
	fmt.Println("=================================")
}

func listenForUserInput() {
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Print("Enter command (add <friendID> / remove <friendID>): ")
		input, _ := reader.ReadString('\n')
		parts := strings.Fields(input)
		if len(parts) != 2 {
			fmt.Println("Invalid command. Use 'add <friendID>' or 'remove <friendID>'")
			continue
		}
		command, friendID := parts[0], strings.TrimSpace(parts[1])

		switch command {
		case "add":
			addFriend(friendID)
		case "remove":
			removeFriend(friendID)
		default:
			fmt.Println("Invalid command. Use 'add <friendID>' or 'remove <friendID>'")
		}
	}
}

func addFriend(friendID string) {
	log.Printf("Adding friend: %s", friendID)
	friendListLock.Lock()
	friendList = append(friendList, friendID)
	friendListLock.Unlock()

	streamLock.Lock()
	defer streamLock.Unlock()

	stream.Send(&pb.FriendListenerRequest{
		Message: &pb.FriendListenerRequest_AddFriend{
			AddFriend: &pb.AddFriend{FriendId: friendID},
		},
	})
}

func removeFriend(friendID string) {
	log.Printf("Removing friend: %s", friendID)
	friendListLock.Lock()
	newFriendList := []string{}
	for _, id := range friendList {
		if id != friendID {
			newFriendList = append(newFriendList, id)
		}
	}
	friendList = newFriendList
	friendListLock.Unlock()

	friendStatusMap.Delete(friendID)

	streamLock.Lock()
	defer streamLock.Unlock()

	stream.Send(&pb.FriendListenerRequest{
		Message: &pb.FriendListenerRequest_RemoveFriend{
			RemoveFriend: &pb.RemoveFriend{FriendId: friendID},
		},
	})
}

// Shared function to dial the gRPC server
func dialServer() (*grpc.ClientConn, error) {
	log.Println("🔗 Dialing gRPC server at komaki.tech:443 ...")
	conn, err := grpc.Dial(
		"komaki.tech:443",
		grpc.WithTransportCredentials(credentials.NewTLS(nil)), // 👈 Secure TLS
		grpc.WithAuthority("komaki.tech"),                      // 👈 SNI for TLS
		grpc.WithBlock(),                                       // 👈 Block until connection is established
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(16*1024*1024),
			grpc.MaxCallSendMsgSize(16*1024*1024),
		),
	)

	if err != nil {
		log.Printf("❌ Failed to connect to gRPC server: %v", err)
		return nil, err
	}
	log.Println("✅ Successfully connected to the gRPC server")
	return conn, nil
}
