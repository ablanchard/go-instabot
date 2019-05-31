package main

import (
	"bufio"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/spf13/viper"
	"gopkg.in/ahmdrz/goinsta.v2"
)

// Insta is a goinsta.Instagram instance
var insta *goinsta.Instagram

// login will try to reload a previous session, and will create a new one if it can't
func login() {
	err := reloadSession()
	if err != nil {
		createAndSaveSession()
	}
}

func syncFollowers() {
	following := insta.Account.Following()
	check(following.Error())
	followers:= insta.Account.Followers()
	check(followers.Error())

	var users []goinsta.User
	for _, user := range following.Users {
		if !contains(followers.Users, user) {
			users = append(users, user)
		}
	}
	fmt.Printf("\n%d users are not following you back!\n", len(users))
	answer := getInput("Do you want to unfollow these users? [yN]")
	if answer != "y" {
		fmt.Println("Not unfollowing.")
		os.Exit(0)
	}
	for _, user := range users {
		fmt.Printf("Unfollowing %s\n", user.Username)
		if !*dev {
			user.SetInstagram(insta)
			err := user.Unfollow()
			check(err)
		}
		time.Sleep(6 * time.Second)
	}
}

func getInput(text string) string {
	reader := bufio.NewReader(os.Stdin)
	fmt.Printf(text)

	input, err := reader.ReadString('\n')
	check(err)
	return strings.TrimSpace(input)
}

// Checks if the user is in the slice
func contains(slice []goinsta.User, user goinsta.User) bool {
	for _, currentUser := range slice {
		if currentUser.ID == user.ID {
			return true
		}
	}
	return false
}

// Logins and saves the session
func createAndSaveSession() {
	insta = goinsta.New(viper.GetString("user.instagram.username"), viper.GetString("user.instagram.password"))
	err := insta.Login()
	check(err)
	err = insta.Export("session")
	check(err)
	log.Println("Created and saved the session")
}

// reloadSession will attempt to recover a previous session
func reloadSession() error {
	if _, err := os.Stat("session"); os.IsNotExist(err) {
		return errors.New("No session found")
	}

	newInsta, err := goinsta.Import("session")
	if err != nil {
		return errors.New("Couldn't recover the session")
	}

	insta = newInsta
	log.Println("Successfully logged in")
	return nil

}

// Go through all the tags in the list
func loopTags() {
	for tag = range tagsList {
		limitsConf := viper.GetStringMap("tags." + tag)
		// Some converting
		limits = map[string]int{
			"follow":  int(limitsConf["follow"].(float64)),
			"like":    int(limitsConf["like"].(float64)),
			"comment": int(limitsConf["comment"].(float64)),
		}
		// What we did so far
		numFollowed = 0
		numLiked = 0
		numCommented = 0

		browse()
	}
	buildReport()
}

// Browses the page for a certain tag, until we reach the limits
func browse() {
	var i = 0
	for numFollowed < limits["follow"] || numLiked < limits["like"] || numCommented < limits["comment"] {
		log.Println("Fetching the list of images for #" + tag + "\n")
		i++

		// Getting all the pictures we can on the first page
		// Instagram will return a 500 sometimes, so we will retry 10 times.
		// Check retry() for more info.
		var feedTag *goinsta.FeedTag
		err := retry(10, 20*time.Second, func() (err error) {
			feedTag, err = insta.Feed.Tags(tag)
			return
		})
		check(err)

		goThrough(feedTag.Images)

		if viper.IsSet("limits.maxRetry") && i > viper.GetInt("limits.maxRetry") {
			log.Println("Currently not enough images for this tag to achieve goals")
			break
		}
	}
}

// Goes through all the images for a certain tag
func goThrough(images []goinsta.Item) {
	var i = 1
	log.Printf("Found %d images\n", len(images))
	for _, image := range images {
		// Skip our own images
		if image.User.Username == viper.GetString("user.instagram.username") {
			continue
		}

		// Check if we should fetch new images for tag
		if i >= limits["follow"] && i >= limits["like"] && i >= limits["comment"] {
			log.Println("Exceed limits for tag, exiting")
			break
		}


		// Getting the user info
		// Instagram will return a 500 sometimes, so we will retry 10 times.
		// Check retry() for more info.
		image.User.SetInstagram(insta)
		err := retry(10, 20*time.Second, func() (err error) {
			err = image.User.Sync()
			return
		})
		check(err)

		poster := &image.User
		followerCount := poster.FollowerCount

		buildLine()

		log.Println("Checking followers for " + poster.Username)
		log.Printf("%s has %d followers, should be between %d and %d\n", poster.Username, followerCount, likeLowerLimit, likeUpperLimit)
		i++

		// Will only follow and comment if we like the picture
		like := followerCount > likeLowerLimit && followerCount < likeUpperLimit && numLiked < limits["like"]
		follow := followerCount > followLowerLimit && followerCount < followUpperLimit && numFollowed < limits["follow"] && like
		comment := followerCount > commentLowerLimit && followerCount < commentUpperLimit && numCommented < limits["comment"] && like

		// Checking if we are already following current user and skipping if we do
		skip := poster.Friendship.Following

		// Like, then comment/follow
		if !skip && like {
			media, err := insta.GetMedia(image.ID)
			check(err)
			likeImage(media.Items[0])
			if follow {
					followUser(poster)
			} else {
				log.Println("Not in the follow global limits, did nothing")
			}
			if comment {
					commentImage(media.Items[0])
			} else {
				log.Println("Not in the comment global limits, did nothing")
			}
		} else {
			log.Println("Not in the like global limits, did nothing")
		}
		log.Printf("%s done\n\n", poster.Username)

		// This is to avoid the temporary ban by Instagram
		time.Sleep(20 * time.Second)
	}
}

// Likes an image, if not liked already
func likeImage(image goinsta.Item) {
	log.Println("Liking the picture")
	if !image.HasLiked {
		if !*dev {
			err := image.Like()
			check(err)
		}
		log.Println("Liked")
		numLiked++
		report[line{tag, "like"}]++
	} else {
		log.Println("Image already liked")
	}
}

// Comments an image
func commentImage(image goinsta.Item) {
	rand.Seed(time.Now().Unix())
	text := commentsList[rand.Intn(len(commentsList))]
	if !*dev {
		err := image.Comments.Add(text)
		check(err)
	}
	log.Println("Commented " + text)
	numCommented++
	report[line{tag, "comment"}]++
}

// Follows a user, if not following already
func followUser(user *goinsta.User) {
	log.Printf("Following %s\n", user.Username)
	userFriendShip := user.Friendship
	// If not following already
	if !userFriendShip.Following {
		if !*dev {
			err := user.Follow()
			check(err)
		}
		log.Println("Followed")
		numFollowed++
		report[line{tag, "follow"}]++
	} else {
		log.Println("Already following " + user.Username)
	}
}
