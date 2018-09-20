# ubuntu-oem-installer

This is a installer for OEM to create factory recovery partition and dump ubuntu OEM image to recovery partition.

## Prerequisites
- ubuntu-oem-image: could be fetched from http://github.com/Lyoncore/ubuntu-oem-installer

## How to build
``` bash
git clone https://github.com/Lyoncore/ubuntu-oem-installer.git
cd ubuntu-oem-installer
go get launchpad.net/godeps
godeps -t -u dependencies.tsv
go run bulid.go build
```

## run tests
``` bash
cd src
go test -check.vv
```
