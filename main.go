package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	goh4 "github.com/remiphilippe/go-h4"
	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/event"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
	"golang.org/x/net/context"
	"io/ioutil"
	"net/url"
	"os"
	"reflect"
	"sort"
	"text/tabwriter"
	"time"
)

const EXPORT_INTERVAL = time.Minute

type VCenter struct {
	Username   string
	Password   string
	URL        string
	Datacenter string
}

type Tetration struct {
	URL    string
	Key    string
	Secret string
}

type Settings struct {
	VCenter   VCenter
	Tetration Tetration
	Insecure  bool
}

func (vc *VCenter) GetURL() (*url.URL, error) {
	u, err := url.Parse(vc.URL)
	u.User = url.UserPassword(vc.Username, vc.Password)
	return u, err
}

type ByName []mo.VirtualMachine

func (n ByName) Len() int           { return len(n) }
func (n ByName) Swap(i, j int)      { n[i], n[j] = n[j], n[i] }
func (n ByName) Less(i, j int) bool { return n[i].Name < n[j].Name }

func exit(err error) {
	fmt.Fprintf(os.Stderr, "Error: %s\n", err)
	os.Exit(1)
}

var subscribeDescription = "Subscribe to VM events (rename and edit tags)"
var subscribeFlag = flag.Bool("subscribe", false, subscribeDescription)

func main() {
	flag.Parse()

	fmt.Println("Loading settings.json")
	file, e := ioutil.ReadFile("./settings.json")
	if e != nil {
		fmt.Printf("File error: %v\n", e)
		os.Exit(1)
	}
	var settings Settings
	json.Unmarshal(file, &settings)
	fmt.Printf("Settings Loaded:\n")
	fmt.Printf(" vCenter:\n")
	fmt.Printf("  %s: %s\n", "URL", settings.VCenter.URL)
	fmt.Printf("  %s: %s\n", "Username", settings.VCenter.Username)
	fmt.Printf("  %s: <hidden>\n", "Password")
	fmt.Printf("  %s: %s\n", "Datacenter", settings.VCenter.Datacenter)
	fmt.Printf(" Tetration:\n")
	fmt.Printf("  %s: %s\n", "URL", settings.Tetration.URL)
	fmt.Printf("  %s: %s\n", "Key", settings.Tetration.Key)
	fmt.Printf("  %s: <hidden>\n", "Secret")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	vcenter, err := settings.VCenter.GetURL()

	// Connect and log in to ESX or vCenter
	c, err := govmomi.NewClient(ctx, vcenter, settings.Insecure)
	if err != nil {
		exit(err)
	}

	f := find.NewFinder(c.Client, true)

	// Find one and only datacenter
	dc, err := f.Datacenter(ctx, settings.VCenter.Datacenter)
	if err != nil {
		exit(err)
	}

	// Make future calls local to this datacenter
	f.SetDatacenter(dc)

	// Find virtual machines in datacenter
	vms, err := f.VirtualMachineList(ctx, "*")
	if err != nil {
		exit(err)
	}

	pc := property.DefaultCollector(c.Client)

	// Convert VMs into list of references
	var refs []types.ManagedObjectReference
	for _, vm := range vms {
		refs = append(refs, vm.Reference())
	}

	// Retrieve name property for all vms
	var vmt []mo.VirtualMachine
	err = pc.Retrieve(ctx, refs, []string{"name", "guest.ipAddress", "customValue"}, &vmt)
	if err != nil {
		exit(err)
	}

	// Use Custom Field Manager to Retrieve int32 -> string mappings for Custom Fields
	mgr := object.NewCustomFieldsManager(c.Client)
	var customFields []types.CustomFieldDef
	customFields, err = mgr.Field(ctx)
	if err != nil {
		exit(err)
	}
	fields := make(map[int32]string)
	for _, field := range customFields {
		fields[field.Key] = field.Name
	}

	// CSV writer
	body := new(bytes.Buffer)
	w := csv.NewWriter(body)
	w.Write([]string{"IP", "VRF", "VM Name", "VM Tags"})

	// Tab writer for nicely formatted output
	tw := tabwriter.NewWriter(os.Stdout, 2, 0, 2, ' ', 0)

	fmt.Println("Virtual machines found:", len(vmt))
	sort.Sort(ByName(vmt))
	for _, vm := range vmt {
		addr := ""
		if vm.Guest != nil && &vm.Guest.IpAddress != nil {
			addr = vm.Guest.IpAddress
			fmt.Fprintf(tw, "%s\t-\t%v\n", addr, vm.Name)
			b := new(bytes.Buffer)
			if len(vm.CustomValue) > 0 {
				fmt.Fprintf(tw, "\tTags\n")
				fmt.Fprintf(tw, "\t==========================\n")
				for _, kv := range vm.CustomValue {
					ref := reflect.ValueOf(kv).Elem()
					val := ref.FieldByName("Value")
					key := fields[kv.GetCustomFieldValue().Key]

					pair := fmt.Sprintf("%s=%s", key, val)
					fmt.Fprintf(tw, "\t%s\n", pair)
					b.WriteString(pair)
					b.WriteString(";")
				}
			}
			w.Write([]string{addr, "Default", vm.Name, b.String()})
		}

	}

	tw.Flush()
	w.Flush()

	fmt.Print("\nUploading current inventory...")

	h4 := new(goh4.H4)
	h4.Secret = settings.Tetration.Secret
	h4.Key = settings.Tetration.Key
	h4.Endpoint = settings.Tetration.URL
	h4.Verify = !settings.Insecure
	h4.Prefix = "/openapi/v1"

	response := h4.Upload(body.Bytes(), true, true)

	fmt.Fprintf(tw, " ...%s\n", response)

	if *subscribeFlag {
		fmt.Println("=================================")
		fmt.Println("Subscribing to VM events")
		fmt.Println("=================================")

		rows := make(chan []string, 1)
		handleEvent := func(ref types.ManagedObjectReference, events []types.BaseEvent) (err error) {
			for _, event := range events {
				switch event.(type) {
				case *types.CustomFieldValueChangedEvent, *types.VmRenamedEvent:
					vmRef := event.GetEvent().Vm.Vm.Reference()
					var vm mo.VirtualMachine
					pc.RetrieveOne(ctx, vmRef, []string{"name", "guest.ipAddress", "customValue"}, &vm)

					if vm.Guest != nil && &vm.Guest.IpAddress != nil {
						addr := vm.Guest.IpAddress
						b := new(bytes.Buffer)
						if len(vm.CustomValue) > 0 {
							for _, kv := range vm.CustomValue {
								ref := reflect.ValueOf(kv).Elem()
								val := ref.FieldByName("Value")
								key := fields[kv.GetCustomFieldValue().Key]

								pair := fmt.Sprintf("%s=%s", key, val)
								fmt.Fprintf(tw, "\t%s\n", pair)
								b.WriteString(pair)
								b.WriteString(";")
							}
						}
						rows <- []string{addr, "Default", vm.Name, b.String()}
						fmt.Printf("Found eligible event for VM: %s (IP: %s) (Tags: %s)\n", vm.Name, addr, b.String())
					}
				}
			}
			return nil
		}

		go func() {
			written := false
			body.Reset()
			w.Write([]string{"IP", "VRF", "VM Name", "VM Tags"})
			for {
				select {
				case row := <-rows:
					w.Write(row)
					written = true
				case <-time.After(EXPORT_INTERVAL):
					if written {
						fmt.Print("Exporting...")
						w.Flush()
						response := h4.Upload(body.Bytes(), true, true)
						fmt.Printf(" ...%s\n", response)
						body.Reset()
						w.Write([]string{"IP", "VRF", "VM Name", "VM Tags"})
						written = false
					}
				}
			}
		}()

		// Setting up the event manager
		refs = []types.ManagedObjectReference{dc.Reference()}
		eventManager := event.NewManager(c.Client)
		err = eventManager.Events(ctx, refs, 10, true, false, handleEvent)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %s\n", err)
			os.Exit(1)
		}
	}
}
