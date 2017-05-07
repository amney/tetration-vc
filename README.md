# Tetration ⇐ vCenter

This application connects to a given vCenter, retrieves all Virtual Machines
that have a valid IP address, recording the name and any associated tags*.

The recorded data is then uploaded to the Tetration Analytics inventory
database as two custom annotations "VM Name" and "VM Tags".

Passing the optional `--subscribe` flag will cause the application to listen for
Virtual Machine rename and tag edit events, pushing any changes to Tetration if necessary.

## Installing

Requirements: `golang 1.7+`

Download the package and dependencies

`go get github.com/amney/tetration-vc`

Change to package location

`cd $GOPATH/src/github.com/amney/tetration-vc`

Copy the example settings file

`cp example.settings.json settings.json`

Edit the settings file, filling in your vCenter and Tetration details

`vi settings.json`

> Note: your Tetration API key must have `user_data_upload` permission

Launch the Tetration ⇐ vCenter App

`go run main.go`


## *Tags
VMWare supports a number of different "tag" options in vCenter. This application
will pull tags that are referred to in the VMWare C# client as "Custom
Attributes".

Prior to 6.5 "Custom Attributes" are not visible in the vCenter Web Client. In
6.5 they are visible. If you are using vCenter version < 6.5 and wish to view
tags in the web client, there are plugins available online to restore functionality.
