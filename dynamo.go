package sharedpw

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/aws/aws-sdk-go/service/dynamodb/dynamodbattribute"
	"log"
	"net"
	"os"
	"regexp"
	"time"
)

// Secret is the structure saved to dynamo.
// 	Secret.Secret is the index, generated by GetRandomId
//	Expire is a unixtime value, for the Dynamo TTL
type Secret struct {
	Secret  string `json:"secret"`
	Expire  int64  `json:"expire"`   // calculated by server, dynamo db removal timestamp in unixtime
	Hours   int    `json:"hours"`    // sent by client, should be < 72
	Message string `json:"message"`
	Ip      string `json:"ip"`
	HasPass bool   `json:"has_pass"`
	Hint    string `json:"hint"`
	Err     error  `json:"error"`
	Tag     string `json:"tag"`
	Iv      string `json:"iv"`
	PwTag   string `json:"pw_tag"`
	PwIv    string `json:"pw_iv"`
}


// NewSecret initializes a Secret -- not sure this is useful outside of tests.
func NewSecret() *Secret {
	s := &Secret{
		Expire: time.Now().UTC().Add(time.Hour * 72).Unix(), // default 3 days lifetime
	}
	id, err := GetRandomId()
	if err != nil {
		s.Err = err
		return s
	}
	s.Secret = id
	return s
}

// NewId sets a new random ID for the secret
func (s *Secret) NewId() (err error) {
	s.Secret, err = GetRandomId()
	return err
}

// SetTimeout sets the expiration timestamp in dynamo based on the hours requested.
func (s *Secret) SetTimeout() (err error) {
	if s.Hours < 1 || s.Hours > 72 {
		return errors.New(`invalid expiration`)
	}
	s.Expire = time.Now().UTC().Add(time.Hour * time.Duration(s.Hours)).Unix()
	return
}

// ToJson returns a JSON string
func (s *Secret) ToJson() (string, error) {
	j, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return "", err
	}
	return string(j), nil
}

// Save persistes a secret into the database
func (s *Secret) Save(b64EncSecret string) error {
	if s.Expire == 0 {
		s.Expire = time.Now().UTC().Add(time.Hour * 24).Unix()
	}
	s.Message = b64EncSecret

	table, db, err := newClient()
	if err != nil {
		return err
	}
	j, err := dynamodbattribute.MarshalMap(s)
	if err != nil {
		return err
	}
	input := &dynamodb.PutItemInput{
		Item:      j,
		TableName: aws.String(table),
	}
	i, err := db.PutItem(input)
	fmt.Printf("%#v\n", i)
	if err != nil {
		log.Printf("| ERROR dynamo.go Save: %v", err)
	}
	fmt.Printf("%#v\n", s)
	return err
}

// Revealed holds the response from a secret lookup
type Revealed struct { 
	Secret string
	Exists bool
	HasPass bool
	Hint string
	Tag string
	Iv string
	PwTag string
	PwIv string
}

// Reveal returns a base64 encoded string of the secret stored in the db, and immediately deletes it.
func Reveal(id string, ip net.IP, reveal bool) (revealed Revealed, err error) {
	dbIndex := `secret`
	notHex, _ := regexp.MatchString(`\W|[g-zA-Z]`, id)
	if len(id) != 16 || notHex {
		return revealed, errors.New("bad id")
	}
	table, db, err := newClient()

	// first get the secret:
	var queryInput = &dynamodb.QueryInput{
		TableName: aws.String(table),
		KeyConditions: map[string]*dynamodb.Condition{
			dbIndex: {
				ComparisonOperator: aws.String("EQ"),
				AttributeValueList: []*dynamodb.AttributeValue{
					{
						S: aws.String(id),
					},
				},
			},
		},
	}
	result, err := db.Query(queryInput)
	if err != nil {
		return revealed, err
	}
	r := make([]interface{}, 0)
	err = dynamodbattribute.UnmarshalListOfMaps(result.Items, &r)
	if err != nil {
		return revealed, err
	}
	if len(r) < 1 {
		return revealed, errors.New("no items found")
	}
	// marshall and unmarshal so we can get the right struct type,
	j, err := json.Marshal(r[0])
	if err != nil {
		return revealed, err
	}
	s := &Secret{}
	err = json.Unmarshal(j, s)

	// check the IP
	if len(s.Ip) > 0 && s.Ip != ip.String() {
		fmt.Printf("IP Mismatch, wanted %s got %s\n", s.Ip, ip.String())
		return revealed, errors.New("not found")
	}

	// are we actually getting the secret, or just checking it exists?
	if !reveal {
		if s.Secret == id {
			revealed.Exists = true
			return revealed, nil
		}
		return revealed, nil
	}

	secret, err := base64.StdEncoding.DecodeString(string(s.Message))
	if err != nil {
		return revealed, errors.New(fmt.Sprintf("could not decode secret %v", err))
	}
	if err != nil || len(secret) == 0 {
		return revealed, errors.New(fmt.Sprintf("could not decode secret, got %d chars and error: %v", len(secret), err))
	}

	// the secret looks okay, but make sure we can delete it before returning ...
	delInput := &dynamodb.DeleteItemInput{
		Key: map[string]*dynamodb.AttributeValue{
			dbIndex: {
				S: aws.String(id),
			},
		},
		TableName: aws.String(table),
	}
	_, err = db.DeleteItem(delInput)
	if err != nil {
		log.Printf("| ERROR db.go DbRemove: %v", err)
		return revealed, err
	}

	revealed.Secret = s.Message
	revealed.Exists = true
	revealed.Hint = s.Hint
	revealed.HasPass = s.HasPass
	revealed.Tag = s.Tag
	revealed.Iv = s.Iv
	revealed.PwTag = s.PwTag
	revealed.PwIv = s.PwIv
	return revealed, nil
}

// newClient returns a table name and dynamodb interface, references the
// environ vars: TABLE and REGION, or uses sane defaults. Defaults to
// "sharedpw" and "us-east-1" respectively.
func newClient() (table string, db *dynamodb.DynamoDB, err error) {
	return func() string {
		if t, ok := os.LookupEnv("APPLICATION"); ok {
			return t
		}
		return "sharedpw"
	}(),
		func() *dynamodb.DynamoDB {
			region, ok := os.LookupEnv("REGION")
			if !ok {
				region = "us-east-1"
			}
			sess, err := session.NewSession(&aws.Config{
				Region: aws.String(region)},
			)
			if err != nil {
				log.Println("| ERROR dynamo.go newClient: did not create session")
			}
			return dynamodb.New(sess)
		}(), err
}
