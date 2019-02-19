/*

v * Copyright (C) 2016 Canonical Ltd
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
	"bufio"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	rplib "github.com/Lyoncore/ubuntu-oem-installer/src/rplib"
)

/*            The partiion layout
 *
 *               u-boot system
 * _________________________________________
 *|                                         |
 *|             GPT/MBR table               |
 *|_________________________________________|
 *|     (Maybe bootloader W/O partitions)   |
 *|           Part 1 (bootloader)           |
 *|_________________________________________|
 *|                                         |
 *|   Part 2 (bootloader or other raw data) |
 *|_________________________________________|
 *|    Part ... (maybe more for raw data)   |
 *|-----------------------------------------|
 *|-----------------------------------------|
 *|         Part X-1 (system-boot)          |
 *|_________________________________________|
 *|                                         |
 *|            Part X (Recovery)            |
 *|_________________________________________|
 *|                                         |
 *|            Part X+1 (writable)          |
 *|_________________________________________|
 *
 *
 *               grub system
 * _________________________________________
 *|                                         |
 *|              GPT/MBR table              |
 *|_________________________________________|
 *|                                         |
 *|            Part 1 (Recovery)            |
 *|_________________________________________|
 *|                                         |
 *|           Part 2 (system-boot)          |
 *|_________________________________________|
 *|                                         |
 *|            Part 3 (writable)            |
 *|_________________________________________|
 */

type Partitions struct {
	// XxxDevNode: sda (W/O partiiton number)
	// XxxDevPath: /dev/sda (W/O partition number)
	SourceDevNode, SourceDevPath                                string
	TargetDevNode, TargetDevPath                                string
	Recovery_nr, Sysboot_nr, Swap_nr, Writable_nr, Last_part_nr int
	Recovery_start, Recovery_end                                int64
	Sysboot_start, Sysboot_end                                  int64
	Swap_start, Swap_end                                        int64
	Writable_start, Writable_end                                int64
	TargetSize                                                  int64
}

const (
	SysbootLabel  = "system-boot"
	WritableLabel = "writable"
	SwapLabel     = "swap"
)

func FindPart(Label string) (devNode string, devPath string, partNr int, err error) {
	partNr = -1
	cmd := exec.Command("findfs", fmt.Sprintf("LABEL=%s", Label))
	out, err := cmd.Output()
	if err != nil {
		return
	}
	fullPath := strings.TrimSpace(string(out[:]))

	if strings.Contains(fullPath, "/dev/") == false {
		err = errors.New(fmt.Sprintf("Label of %q not found", Label))
		return
	}
	devPath = fullPath

	// The devPath is with partiion /dev/sdX1 or /dev/mmcblkXp1
	// Here to remove the partition information
	for {
		if _, err := strconv.Atoi(string(devPath[len(devPath)-1])); err == nil {
			devPath = devPath[:len(devPath)-1]
		} else {
			part_nr := strings.TrimPrefix(fullPath, devPath)
			if partNr, err = strconv.Atoi(part_nr); err != nil {
				err = errors.New("Unknown error while FindPart")
				return "", "", -1, err
			}
			if devPath[len(devPath)-1] == 'p' {
				devPath = devPath[:len(devPath)-1]
			}
			break
		}
	}

	field := strings.Split(devPath, "/")
	devNode = field[len(field)-1]

	return
}

func FindTargetParts(parts *Partitions) error {
	var devPath string
	if parts.SourceDevNode == "" || parts.SourceDevPath == "" || parts.Recovery_nr == -1 {
		return fmt.Errorf("Missing source recovery data")
	}

	// If config.yaml has set the specific recovery device,
	// it would use is as recovery device.
	// Or it would find out the recovery device
	if configs.Recovery.RecoveryDevice != "" {
		parts.TargetDevPath = configs.Recovery.RecoveryDevice
		parts.TargetDevNode = filepath.Base(parts.TargetDevPath)
	} else {
		// target disk might raid devices (/dev/md126)
		if _, err := os.Stat("/sys/block/md126/dev"); err == nil {
			log.Println("found raid devices enabled in BIOS")
			dat := []byte("")
			dat, err := ioutil.ReadFile("/sys/block/md126/dev")
			if err != nil {
				return err
			}
			dat_str := strings.TrimSpace(string(dat))
			blockDevice := rplib.Realpath(fmt.Sprintf("/dev/block/%s", dat_str))
			if blockDevice != parts.SourceDevPath {
				parts.TargetDevPath = blockDevice
				parts.TargetDevNode = filepath.Base(parts.TargetDevPath)
				return nil
			}
		}

		// target disk might be emmc
		if devPath == "" {
			blockArray, _ := filepath.Glob("/sys/block/mmcblk*")
			for _, block := range blockArray {
				dat := []byte("")
				dat, err := ioutil.ReadFile(filepath.Join(block, "dev"))
				if err != nil {
					return err
				}
				dat_str := strings.TrimSpace(string(dat))
				blockDevice := rplib.Realpath(fmt.Sprintf("/dev/block/%s", dat_str))
				if blockDevice != parts.SourceDevPath {
					devPath = blockDevice
					if devPath == "/dev/mmcblk0" {
						parts.TargetDevPath = devPath
						parts.TargetDevNode = filepath.Base(parts.TargetDevPath)
						return nil
					}
					break
				}
			}
		}

		// target disk might be scsi disk
		if devPath == "" {
			blockArray, _ := filepath.Glob("/sys/block/sd*")
			for _, block := range blockArray {
				dat := []byte("")
				dat, err := ioutil.ReadFile(filepath.Join(block, "dev"))
				if err != nil {
					return err
				}
				dat_str := strings.TrimSpace(string(dat))
				blockDevice := rplib.Realpath(fmt.Sprintf("/dev/block/%s", dat_str))

				if blockDevice != parts.SourceDevPath {
					devPath = blockDevice
					break
				}
			}
		}

		// target disk might be nvme disk
		if devPath == "" {
			blockArray, _ := filepath.Glob("/sys/block/nvme*")
			for _, block := range blockArray {
				dat := []byte("")
				dat, err := ioutil.ReadFile(filepath.Join(block, "dev"))
				if err != nil {
					return err
				}
				dat_str := strings.TrimSpace(string(dat))
				blockDevice := rplib.Realpath(fmt.Sprintf("/dev/block/%s", dat_str))

				if blockDevice != parts.SourceDevPath {
					devPath = blockDevice
					break
				}
			}
		}

		if devPath != "" {
			// The devPath is with partiion for /dev/sdX1 or /dev/mmcblkXp1
			// but without partition for /dev/nvmeXnX
			// Here to remove the partition information
			for {
				if true == strings.Contains(devPath, "nvme") {
					parts.TargetDevPath = devPath
					parts.TargetDevNode = filepath.Base(parts.TargetDevPath)
					break
				}
				if _, err := strconv.Atoi(string(devPath[len(devPath)-1])); err == nil {
					devPath = devPath[:len(devPath)-1]
				} else {
					if devPath[len(devPath)-1] == 'p' {
						devPath = devPath[:len(devPath)-1]
					}
					parts.TargetDevPath = devPath
					parts.TargetDevNode = filepath.Base(parts.TargetDevPath)
					break
				}
			}
			log.Println("debug: ", parts.TargetDevPath, parts.TargetDevNode)
		} else {
			return fmt.Errorf("No target disk found")
		}
	}
	return nil
}

var parts Partitions

func GetPartitions(recoveryLabel string) (*Partitions, error) {
	var err error
	const OLD_PARTITION = "/tmp/old-partition.txt"
	parts = Partitions{"", "", "", "", -1, -1, -1, -1, -1, 0, 20479, -1, -1, -1, -1, -1, -1, -1}

	//The Source device which must has a recovery partition
	parts.SourceDevNode, parts.SourceDevPath, parts.Recovery_nr, err = FindPart(recoveryLabel)
	if err != nil {
		err = errors.New(fmt.Sprintf("Recovery partition (LABEL=%s) not found", recoveryLabel))
		return nil, err
	}

	err = FindTargetParts(&parts)
	if err != nil {
		err = errors.New(fmt.Sprintf("Target install partition not found"))
		parts = Partitions{"", "", "", "", -1, -1, -1, -1, -1, 0, 20479, -1, -1, -1, -1, -1, -1, -1}
		return nil, err
	}

	//system-boot partition info
	devnode, _, sysboot_nr, err := FindPart(SysbootLabel)
	if err == nil {
		if parts.SourceDevNode != devnode {
			//Target system-boot found and must not source device in headless_installer mode
			parts.Sysboot_nr = sysboot_nr
		}
	}

	//swap partition info
	_, _, parts.Swap_nr, err = FindPart(SwapLabel)
	if err != nil {
		//Partition not found, keep value in '-1'
		parts.Swap_nr = -1
	}

	//writable-boot partition info
	_, _, parts.Writable_nr, err = FindPart(WritableLabel)
	if err != nil {
		//Partition not found, keep value in '-1'
		parts.Writable_nr = -1
	}

	if parts.Recovery_nr == -1 && parts.Sysboot_nr == -1 && parts.Writable_nr == -1 {
		//Noting to find, return.
		return &parts, nil
	}

	// find out detail information of each partition
	cmd := exec.Command("parted", "-ms", fmt.Sprintf("/dev/%s", parts.TargetDevNode), "unit", "B", "print")
	stdout, _ := cmd.StdoutPipe()
	cmd.Start()
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Split(line, ":")

		// get disk size
		if strings.Contains(fields[0], "/dev/") == true {
			parts.TargetSize, err = strconv.ParseInt(strings.TrimRight(fields[1], "B"), 10, 64)
			if err != nil {
				fmt.Errorf("Parsing disk size failed")
			}
			continue
		}

		nr, err := strconv.Atoi(fields[0])
		if err != nil { //ignore the line don't neeed
			continue
		}
		end, err := strconv.ParseInt(strings.TrimRight(fields[2], "B"), 10, 64)

		if err != nil {
			return nil, err
		}

		start, err := strconv.ParseInt(strings.TrimRight(fields[1], "B"), 10, 64)

		if err != nil {
			return nil, err
		}

		if parts.SourceDevPath == parts.TargetDevPath {
			if parts.Recovery_nr != -1 && parts.Recovery_nr == nr {
				parts.Recovery_start = start
				parts.Recovery_end = end
			}
		}
		parts.Last_part_nr = nr
	}
	cmd.Wait()
	err = scanner.Err()
	if err != nil {
		return nil, err
	}
	return &parts, nil
}

func SetPartitionStartEnd(parts *Partitions, partName string, partSizeMB int, bootloader string) error {
        if parts == nil {
                return fmt.Errorf("nil Partitions")
        }

        switch partName {
        case "system-boot":
                if bootloader == "u-boot" {
                        // Not allow to edit system-boot in u-boot yet.
                } else if bootloader == "grub" {
                        parts.Sysboot_start = parts.Recovery_end + 1
                        parts.Sysboot_end = parts.Sysboot_start + int64(partSizeMB*1024*1024)
                }
                //TODO: To support swap partition
        case "swap":
                if bootloader == "u-boot" {
                        // Not allow to edit swap in u-boot yet.
                } else if bootloader == "grub" {
                        parts.Swap_start = parts.Sysboot_end + 1
                        parts.Swap_end = parts.Swap_start + int64(partSizeMB*1024*1024)
                }
                // The writable partition would be enlarged to maximum.
                // Here does not support change the Start, End
        default:
                return fmt.Errorf("Unknown Partition Name %s", partName)
        }

        return nil
}

func CopyRecoveryPart(parts *Partitions) error {
	parts.Recovery_nr = 1
	recoveryBegin := 4
	if configs.Recovery.RecoverySize <= 0 {
		return fmt.Errorf("Invalid recovery size: %d", configs.Recovery.RecoverySize)
	}
	recoveryEnd := recoveryBegin + configs.Recovery.RecoverySize

	// Build Recovery Partition
	recovery_path := fmtPartPath(parts.TargetDevPath, parts.Recovery_nr)
	rplib.Shellexec("parted", "-ms", "-a", "optimal", parts.TargetDevPath,
		"unit", "MiB",
		"mklabel", "gpt",
		"mkpart", "primary", "fat32", fmt.Sprintf("%d", recoveryBegin), fmt.Sprintf("%d", recoveryEnd),
		"name", fmt.Sprintf("%v", parts.Recovery_nr), configs.Recovery.FsLabel,
		"set", fmt.Sprintf("%v", parts.Recovery_nr), "boot", "on",
		"print")
	exec.Command("partprobe").Run()
	rplib.Shellexec("sleep", "2") //wait the partition presents
	rplib.Shellexec("mkfs.vfat", "-F", "32", "-n", configs.Recovery.FsLabel, recovery_path)

	// Copy recovery data
	err := os.MkdirAll(RECO_TAR_MNT_DIR, 0755)
	if err != nil {
		return err
	}
	err = syscall.Mount(recovery_path, RECO_TAR_MNT_DIR, "vfat", 0, "")
	if err != nil {
		return err
	}
	defer syscall.Unmount(RECO_TAR_MNT_DIR, 0)
	rplib.Shellcmd(fmt.Sprintf("rsync -aH %s %s", RECO_ROOT_DIR, RECO_TAR_MNT_DIR))
	rplib.Shellexec("sync")

	// set target grubenv to factory_restore
	if _, err = os.Stat(SYSBOOT_MNT_DIR + "EFI"); err == nil {
		cmd := exec.Command("grub-editenv", filepath.Join(RECO_TAR_MNT_DIR, "EFI/ubuntu/grubenv"), "set", "recovery_type=factory_install")
		cmd.Run()
	} else if _, err = os.Stat(SYSBOOT_MNT_DIR + "efi"); err == nil {
		cmd := exec.Command("grub-editenv", filepath.Join(RECO_TAR_MNT_DIR, "efi/ubuntu/grubenv"), "set", "recovery_type=factory_install")
		cmd.Run()
	}

	return nil
}

func InstallSystemPart(parts *Partitions) error {
	log.Println("InstallSystemPart...")

	log.Println("Recreate partition table")
	var dev_path string = strings.Replace(parts.TargetDevPath, "mapper/", "", -1)
	//part_nr := parts.Last_part_nr
	parts.Sysboot_nr = 1 //If target device is not same as source, the system-boot parition will start from 1st partition

	if configs.Configs.Swap == true && configs.Configs.SwapFile != true && configs.Configs.SwapSize > 0 {
		parts.Swap_nr = parts.Sysboot_nr + 1  //swap is one after system-boot
		parts.Writable_nr = parts.Swap_nr + 1 //writable is one after swap
	} else {
		parts.Swap_nr = -1                       //swap is not enabled
		parts.Writable_nr = parts.Sysboot_nr + 1 //writable is one after system-boot
	}
	rplib.Shellexec("sgdisk", dev_path, "--randomize-guids", "--move-second-header") // partType == "gpt"
	rplib.Shellexec("parted", "-ms", dev_path, "mklabel", "gpt")                     // Build a new GPT to remove all partitions if target device is another disk


	log.Println("Create Boot partition")
	sysboot_path := fmtPartPath(parts.TargetDevPath, parts.Sysboot_nr)
	rplib.Shellexec("parted", "-a", "optimal", "-ms", dev_path, "--", "mkpart", "primary", "fat32",
		fmt.Sprintf("%vB", parts.Sysboot_start), fmt.Sprintf("%vB", parts.Sysboot_end), "name", fmt.Sprintf("%v", parts.Sysboot_nr), SysbootLabel)
	rplib.Shellexec("udevadm", "settle")
	exec.Command("partprobe").Run()
	rplib.Shellexec("sleep", "2") //wait the partition presents
	rplib.Shellexec("mkfs.vfat", "-F", "32", "-n", SysbootLabel, sysboot_path)

	err := os.MkdirAll(SYSBOOT_MNT_DIR, 0755)
	rplib.Checkerr(err)
	err = syscall.Mount(sysboot_path, SYSBOOT_MNT_DIR, "vfat", 0, "")
	rplib.Checkerr(err)
	defer syscall.Unmount(SYSBOOT_MNT_DIR, 0)

	//TODO: Install boot
        // Copy /boot data on src partition 2
        sysboot_src_path := fmtPartPath(parts.SourceDevPath, 2) //MAGIC: this is src part sysboot

        err = os.MkdirAll(SOURCE_SYSBOOT_MNT_DIR, 0755)
        rplib.Checkerr(err)
        err = syscall.Mount(sysboot_src_path, SOURCE_SYSBOOT_MNT_DIR, "vfat", 0, "")
        rplib.Checkerr(err)
        defer syscall.Unmount(SOURCE_SYSBOOT_MNT_DIR, 0)

        rplib.Shellcmd(fmt.Sprintf("rsync -aH %s %s", SOURCE_SYSBOOT_MNT_DIR, SYSBOOT_MNT_DIR))
        rplib.Shellexec("sync")

	rplib.Shellexec("parted", "-ms", dev_path, "set", strconv.Itoa(parts.Sysboot_nr), "boot", "on") //CHECK: efi should not need this.

	// Create swap partition
	if configs.Configs.Swap == true && configs.Configs.SwapFile != true && configs.Configs.SwapSize > 0 {
		log.Println("Create Swap partition")
		_, new_end := rplib.GetPartitionBeginEnd(dev_path, parts.Sysboot_nr)
		parts.Swap_start = int64(new_end + 1)

		rplib.Shellexec("parted", "-a", "optimal", "-ms", dev_path, "--", "mkpart", "primary", "linux-swap", fmt.Sprintf("%vB", parts.Swap_start), fmt.Sprintf("%vB", parts.Swap_end), "name", fmt.Sprintf("%v", parts.Swap_nr), SwapLabel)
		rplib.Shellexec("udevadm", "settle")
		rplib.Shellexec("mkswap", fmtPartPath(parts.TargetDevPath, parts.Swap_nr))
	}

	// Restore writable
	log.Println("Create writable partition")
	var new_end int
	if configs.Configs.Swap == true && configs.Configs.SwapFile != true && configs.Configs.SwapSize > 0 {
		_, new_end = rplib.GetPartitionBeginEnd(dev_path, parts.Swap_nr)
	} else {
		_, new_end = rplib.GetPartitionBeginEnd(dev_path, parts.Sysboot_nr)
	}

	parts.Writable_start = int64(new_end + 1)
	var writable_start string = fmt.Sprintf("%vB", parts.Writable_start)
	var writable_nr string = strconv.Itoa(parts.Writable_nr)
	writable_path := fmtPartPath(parts.TargetDevPath, parts.Writable_nr)

	rplib.Shellexec("parted", "-a", "optimal", "-ms", dev_path, "--", "mkpart", "primary", "ext4", writable_start, "100%", "name", writable_nr, WritableLabel)

	rplib.Shellexec("udevadm", "settle")
	exec.Command("partprobe").Run()
	rplib.Shellexec("modprobe","dm_crypt")
	rplib.Shellexec("sleep", "2") //wait the partition presents

	//TODO: install writable

	rplib.Shellcmd("echo 1234 > /tmp/mykeyfile")
	rplib.Shellexec("cryptsetup","luksFormat",writable_path, "/tmp/mykeyfile")
	rplib.Shellexec("cryptsetup","--key-file","/tmp/mykeyfile","open",writable_path,"cryptroot")

	//setup new writablepath
	writable_path = "/dev/mapper/cryptroot"

	rplib.Shellexec("mkfs.ext4", "-F", "-L", WritableLabel, writable_path)

	err = os.MkdirAll(WRITABLE_MNT_DIR, 0755)
	rplib.Checkerr(err)
	err = syscall.Mount(writable_path, WRITABLE_MNT_DIR, "ext4", 0, "")
	rplib.Checkerr(err)
	defer syscall.Unmount(WRITABLE_MNT_DIR, 0)

	writable_src_path := fmtPartPath(parts.SourceDevPath, 3) //MAGIC: this is src part writable

	err = os.MkdirAll(SOURCE_WRITABLE_MNT_DIR, 0755)
	rplib.Checkerr(err)
	err = syscall.Mount(writable_src_path, SOURCE_WRITABLE_MNT_DIR, "ext4", 0, "")
	rplib.Checkerr(err)
	defer syscall.Unmount(SOURCE_WRITABLE_MNT_DIR, 0)

        rplib.Shellcmd(fmt.Sprintf("rsync -aH %s %s", SOURCE_WRITABLE_MNT_DIR, WRITABLE_MNT_DIR))
        rplib.Shellexec("sync")

        rplib.Shellexec("tpm2_pcrlist","-T","device:/dev/tpmrm0")

	log.Println("Finish InstallSystemPart...")
	return nil
}
