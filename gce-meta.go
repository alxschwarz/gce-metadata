package main

import (
	"encoding/json"
	"fmt"
	"gopkg.in/urfave/cli.v1"
	"io/ioutil"
	"net/http"
	"os"
	"strings"
	"sync"
)

type NotDefinedError string

func (suffix NotDefinedError) Error() string {
	return fmt.Sprintf("metadata: GCE metadata %q not defined", string(suffix))
}

func Get(suffix string) (string, error) {
	val, _, err := getETag(suffix)
	return val, err
}

func getETag(suffix string) (value, etag string, err error) {
	// Using a fixed IP makes it very difficult to spoof the metadata service in
	// a container, which is an important use-case for local testing of cloud
	// deployments. To enable spoofing of the metadata service, the environment
	// variable GCE_METADATA_HOST is first inspected to decide where metadata
	// requests shall go.
	client := &http.Client{}
	host := os.Getenv("GCE_METADATA_HOST")
	if host == "" {
		// Using 169.254.169.254 instead of "metadata" here because Go
		// binaries built with the "netgo" tag and without cgo won't
		// know the search suffix for "metadata" is
		// ".google.internal", and this IP address is documented as
		// being stable anyway.
		host = "169.254.169.254"
	}
	url := "http://" + host + "/computeMetadata/v1/" + suffix
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Metadata-Flavor", "Google")
	res, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return "", "", NotDefinedError(suffix)
	}
	if res.StatusCode != 200 {
		return "", "", fmt.Errorf("status code %d trying to fetch %s", res.StatusCode, url)
	}
	all, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return "", "", err
	}
	return string(all), res.Header.Get("Etag"), nil
}

func getTrimmed(suffix string) (s string, err error) {
	s, err = Get(suffix)
	s = strings.TrimSpace(s)
	return
}

var onGCE struct {
	sync.Mutex
	set bool
	v   bool
}

// OnGCE reports whether this process is running on Google Compute Engine.
func OnGCE() bool {
	defer onGCE.Unlock()
	onGCE.Lock()
	if onGCE.set {
		return onGCE.v
	}
	onGCE.set = true

	client := &http.Client{}

	// We use the DNS name of the metadata service here instead of the IP address
	// because we expect that to fail faster in the not-on-GCE case.
	res, err := client.Get("http://metadata.google.internal")
	if err != nil {
		return false
	}
	onGCE.v = res.Header.Get("Metadata-Flavor") == "Google"
	return onGCE.v
}

// ProjectID returns the current instance's project ID string.
func ProjectID() (string, error) {
	return getTrimmed("project/project-id")
}

// NumericProjectID returns the current instance's numeric project ID.
func NumericProjectID() (string, error) {
	return getTrimmed("project/numeric-project-id")
}

// InternalIP returns the instance's primary internal IP address.
func InternalIP() (string, error) {
	return getTrimmed("instance/network-interfaces/0/ip")
}

// ExternalIP returns the instance's primary external (public) IP address.
func ExternalIP() (string, error) {
	return getTrimmed("instance/network-interfaces/0/access-configs/0/external-ip")
}

// Hostname returns the instance's hostname. This will be of the form
// "<instanceID>.c.<projID>.internal".
func Hostname() (string, error) {
	return getTrimmed("instance/hostname")
}

// MachineType returns the instance's machine type.
func MachineType() (string, error) {
	machine, err := getTrimmed("instance/machine-type")
	// machine-type is of the form "projects/<projNum>/machineTypes/<machine-typeName>
	if err != nil {
		return "", err
	}
	return machine[strings.LastIndex(machine, "/")+1:], nil
}

// Description returns the instance's description.
func Description() (string, error) {
	return getTrimmed("instance/description")
}

// InstanceTags returns the list of user-defined instance tags,
// assigned when initially creating a GCE instance.
func InstanceTags() ([]string, error) {
	var s []string
	j, err := Get("instance/tags")
	if err != nil {
		return nil, err
	}
	if err := json.NewDecoder(strings.NewReader(j)).Decode(&s); err != nil {
		return nil, err
	}
	return s, nil
}

// InstanceID returns the current VM's numeric instance ID.
func InstanceID() (string, error) {
	return getTrimmed("instance/id")
}

// InstanceName returns the current VM's instance ID string.
func InstanceName() (string, error) {
	host, err := Hostname()
	if err != nil {
		return "", err
	}
	return strings.Split(host, ".")[0], nil
}

// Zone returns the current VM's zone, such as "us-central1-b".
func Zone() (string, error) {
	zone, err := getTrimmed("instance/zone")
	// zone is of the form "projects/<projNum>/zones/<zoneName>".
	if err != nil {
		return "", err
	}
	return zone[strings.LastIndex(zone, "/")+1:], nil
}

// InstanceAttributes returns the list of user-defined attributes,
// assigned when initially creating a GCE VM instance. The value of an
// attribute can be obtained with InstanceAttributeValue.
func InstanceAttributes() ([]string, error) { return lines("instance/attributes/") }

func lines(suffix string) ([]string, error) {
	j, err := Get(suffix)
	if err != nil {
		return nil, err
	}
	s := strings.Split(strings.TrimSpace(j), "\n")
	for i := range s {
		s[i] = strings.TrimSpace(s[i])
	}
	return s, nil
}

// InstanceAttributeValue returns the value of the provided VM
// instance attribute.
//
// If the requested attribute is not defined, the returned error will
// be of type NotDefinedError.
//
// InstanceAttributeValue may return ("", nil) if the attribute was
// defined to be the empty string.
func InstanceAttributeValue(attr string) (string, error) {
	return Get("instance/attributes/" + attr)
}

func main() {
	app := cli.NewApp()

	app.Version = "0.2"

	if len(os.Args) < 2 || len(os.Args) > 2 {
		app.Action = func(c *cli.Context) error {
			cli.ShowAppHelp(c)
			return nil
		}
	}

	app.Commands = []cli.Command{
		{
			Name:  "project-id",
			Usage: "The project ID.",
			Action: func(c *cli.Context) error {
				id, err := ProjectID()
				if err != nil {
					return err
				}
				fmt.Println(id)
				return nil
			},
		},
		{
			Name:  "numeric-project-id",
			Usage: "The numeric project ID of the instance, which is not the same as the project name visible in the Google Cloud Platform Console.",
			Action: func(c *cli.Context) error {
				id, err := NumericProjectID()
				if err != nil {
					return err
				}
				fmt.Println(id)
				return nil
			},
		},
		{
			Name:  "desc",
			Usage: "The free-text description of an instance, assigned using the --description flag, or set in the API.",
			Action: func(c *cli.Context) error {
				desc, err := Description()
				if err != nil {
					return err
				}
				fmt.Println(desc)
				return nil
			},
		},
		{
			Name:  "hostname",
			Usage: "The full host name of the instance.",
			Action: func(c *cli.Context) error {
				hostname, err := Hostname()
				if err != nil {
					return err
				}
				fmt.Println(hostname)
				return nil
			},
		},
		{
			Name:  "instance-name",
			Usage: "The short host name of the instance.",
			Action: func(c *cli.Context) error {
				name, err := InstanceName()
				if err != nil {
					return err
				}
				fmt.Println(name)
				return nil
			},
		},
		{
			Name:  "instance-id",
			Usage: "The ID of the instance. This is a unique, numerical ID that is generated by Google Compute Engine.",
			Action: func(c *cli.Context) error {
				id, err := InstanceID()
				if err != nil {
					return err
				}
				fmt.Println(id)
				return nil
			},
		},
		{
			Name:  "machine-type",
			Usage: "The fully-qualified machine type name of the instance's host machine.",
			Action: func(c *cli.Context) error {
				machine, err := MachineType()
				if err != nil {
					return err
				}
				fmt.Println(machine)
				return nil
			},
		},
		{
			Name:  "zone",
			Usage: "The instance's zone.",
			Action: func(c *cli.Context) error {
				zone, err := Zone()
				if err != nil {
					return err
				}
				fmt.Println(zone)
				return nil
			},
		},
	}

	app.Run(os.Args)
}
