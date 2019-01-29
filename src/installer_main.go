// -*- Mode: Go; indent-tabs-mode: t -*-

/*
 * Copyright (C) 2016-2017 Canonical Ltd
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License version 3 as
 * published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program.  If not, see <http://www.gnu.org/licenses/>.
 *
 */

package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	rplib "github.com/Lyoncore/ubuntu-oem-installer/src/rplib"
)

var version string
var commit string
var commitstamp string
var build_date string

const (
	RECO_ROOT_DIR           = "/run/recovery/"
	CONFIG_YAML             = RECO_ROOT_DIR + "recovery/config.yaml"
	RECO_TAR_MNT_DIR        = "/tmp/recoMnt/"
	SYSBOOT_MNT_DIR         = "/tmp/system-boot/"
	WRITABLE_MNT_DIR        = "/tmp/writableMnt/"
	SOURCE_SYSBOOT_MNT_DIR  = "/tmp/src/system-boot/"
	SOURCE_WRITABLE_MNT_DIR = "/tmp/src/writableMnt/"
)

var configs rplib.ConfigRecovery

func parseConfigs(configFilePath string) {
	var configPath string
	if "" == configFilePath {
		configPath = CONFIG_YAML
	} else {
		configPath = configFilePath
	}

	if "" == version {
		version = Version
	}

	commitstampInt64, _ := strconv.ParseInt(commitstamp, 10, 64)
	log.Printf("Version: %v, Commit: %v, Build date: %v\n", version, commit, time.Unix(commitstampInt64, 0).UTC())

	// Load config.yaml
	err := configs.Load(configPath)
	rplib.Checkerr(err)
	log.Println(configs)
}

// easier for function mocking
var getPartitions = GetPartitions

func main() {
	log.Printf("Ubuntu OEM installer Start.")
	flag.Parse()
	if len(flag.Args()) != 1 {
		log.Panicf(fmt.Sprintf("Need a argument of [INSTALLER_LABEL]. Current arguments: %v", flag.Args()))
	}
	InstallerLabel := flag.Arg(0)
	log.Printf("Installer Label: %s", InstallerLabel)

	// setup if now is ubuntu server curtin image
	err := envForUbuntuClassic()
	if err != nil {
		os.Exit(-1)
	}

	log.Println("parse YAML")
	parseConfigs(CONFIG_YAML)

	log.Println("Find boot device, all other partiitons info")
	parts, err := getPartitions(InstallerLabel)

	if err != nil {
		log.Panicf("Installer partition not found, error: %s\n", err)
	}

	log.Println(parts)
	log.Printf("configs.Recovery.Type is %s\n", configs.Recovery.Type)

	if parts.SourceDevPath == parts.TargetDevPath {
		log.Panicf("The source device and target device are same")
	}

	// The bootsize must larger than 50MB
	if configs.Configs.BootSize >= 50 {
		SetPartitionStartEnd(parts, SysbootLabel, configs.Configs.BootSize, configs.Configs.Bootloader)
	} else {
		log.Println("Invalid bootsize in config.yaml:", configs.Configs.BootSize)
	}

	if configs.Configs.Swap == true && configs.Configs.SwapFile != true && configs.Configs.SwapSize > 0 {
		SetPartitionStartEnd(parts, SwapLabel, configs.Configs.SwapSize, configs.Configs.Bootloader)
	}

	if configs.Recovery.Type == rplib.INSTALLER_ONLY {
		err = InstallSystemPart(parts)
		if err != nil {
			os.Exit(-1)
		}
	} else {
		err = CopyRecoveryPart(parts)
		if err != nil {
			os.Exit(-1)
		}
	}
	os.Exit(0)
}
