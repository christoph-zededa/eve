// Copyright (c) 2026 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/lf-edge/eve/evetest/broker/provider"
	api "github.com/lf-edge/eve/evetest/grpcapi/go"
	"github.com/lf-edge/eve/evetest/utils"
	"github.com/sirupsen/logrus"
)

// buildSdnImage builds an SDN disk image by running the evetest-sdn Docker image
// for a given architecture.
//
// Parameters:
//   - imageDirPath: Path to the output directory.
//   - dockerImageName: Name of the (multi-arch) evetest-sdn Docker image to run.
//   - arch: Target architecture for which to build the image.
//
// Steps:
//  1. Ensures the target directory exists.
//  2. Run the given multi-arch SDN docker image under the specified architecture.
//  3. Runs the Docker container with appropriate args and mounts to generate the SDN image.
//
// Returns an error if the build or any Docker operation fails.
func buildSdnImage(ctx context.Context, log *logrus.Entry, imageDirPath,
	dockerImageName string, arch api.ArchType) (imageSpec provider.ImageSpec, err error) {

	// Ensure the target directory exists.
	if err = os.MkdirAll(imageDirPath, 0o755); err != nil {
		err = fmt.Errorf("failed to create SDN image directory %q: %w", imageDirPath, err)
		return imageSpec, err
	}
	defer func() {
		if err != nil {
			if removeErr := os.RemoveAll(imageDirPath); removeErr != nil {
				log.Warnf("Failed to remove SDN image directory %q : %v",
					imageDirPath, removeErr)
			}
		}
	}()

	// Determine Docker platform string for the given architecture.
	var platform string
	switch arch {
	case api.ArchType_ARCH_AMD64:
		platform = "linux/amd64"
	case api.ArchType_ARCH_ARM64:
		platform = "linux/arm64"
	default:
		err = fmt.Errorf("unsupported architecture: %s", arch)
		return imageSpec, err
	}

	// Construct the command to run inside the container.
	cmd := "-f qcow2 image"

	// Build the volume mapping: container target → host source.
	volumeMap := map[string]string{
		"/out": imageDirPath,
	}

	// Run the SDN docker container to build the SDN Qcow2 image.
	imageSpec.Qcow2ImagePath = filepath.Join(imageDirPath, "evetest-sdn.img.qcow2")
	log.Infof("Building SDN image into the file %q", imageSpec.ImageFilePath())
	result, err := utils.RunDockerCommand(
		ctx, log, dockerImageName, cmd, volumeMap, platform)
	if err != nil {
		err = fmt.Errorf("failed to run docker command for SDN image build: %w", err)
		return imageSpec, err
	}

	// Check that the generated image file exists and is non-empty
	info, statErr := os.Stat(imageSpec.ImageFilePath())
	if statErr != nil {
		log.Infof("Docker output:\n%s", result)
		err = fmt.Errorf("expected SDN image file %q not found: %w",
			imageSpec.ImageFilePath(), statErr)
		return imageSpec, err
	}
	if info.Size() == 0 {
		log.Infof("Docker output:\n%s", result)
		err = fmt.Errorf("SDN image file %q is empty", imageSpec.ImageFilePath())
		return imageSpec, err
	}

	log.Infof("Successfully built SDN image from %s for %s: %s",
		dockerImageName, arch, imageSpec.ImageFilePath())
	log.Debugf("Docker output:\n%s", result)
	return imageSpec, nil
}

const (
	// liveDiskPartSpec is the partition list make-raw is asked for when assembling a
	// live device disk. Mirrors PART_SPEC in do_live() of pkg/eve/runme.sh.
	liveDiskPartSpec = "efi conf imga"

	// defaultLiveDiskSizeMB is used when the caller does not request a disk size.
	// Mirrors DEFAULT_LIVE_IMG_SIZE in pkg/eve/runme.sh.
	defaultLiveDiskSizeMB = 28762
)

// assembleLiveDiskScript reproduces what do_live() in pkg/eve/runme.sh does, for
// execution inside EVE's mkimage-raw-efi package image. Config files staged in /in
// are copied into the config partition, a soft serial number is generated if the
// partition does not already carry one (a live image is normally given one by the
// installer, which is skipped here), and make-raw then assembles the disk.
const assembleLiveDiskScript = `
set -e
if [ -n "$(ls -A /in 2>/dev/null)" ]; then
	mcopy -o -i /parts/config.img -s /in/* ::/
fi
if ! mcopy -o -i /parts/config.img ::/soft_serial /tmp 2>/dev/null; then
	uuidgen > /tmp/soft_serial
	mcopy -o -i /parts/config.img /tmp/soft_serial ::/soft_serial
fi
echo "soft_serial=$(cat /tmp/soft_serial)"
truncate -s %dM /out/live.raw
/make-raw /out/live.raw "%s"
`

// eveBuildDirOf returns the EVE build directory holding the given rootfs image, i.e.
// the dist/<arch>/current/installer directory it was built into. Empty in, empty out.
func eveBuildDirOf(rootfsPath string) string {
	if rootfsPath == "" {
		return ""
	}
	return filepath.Dir(rootfsPath)
}

// eveBuildHasDiskParts reports whether an EVE build directory holds everything needed
// to assemble a device disk without falling back to an EVE container image. Note that
// "make rootfs" alone does not: grub, u-boot, the UEFI firmware and the config
// partition belong to the "diskparts", "live" and "eve" targets.
func eveBuildHasDiskParts(bitsDir string) bool {
	if bitsDir == "" {
		return false
	}
	if !dirExists(filepath.Join(bitsDir, "EFI")) {
		return false
	}
	if !dirExists(filepath.Join(bitsDir, "firmware")) {
		return false
	}
	info, err := os.Stat(filepath.Join(bitsDir, "config.img"))
	return err == nil && info.Mode().IsRegular()
}

// eveDiskPayloads are the artifacts that make-raw writes onto the device disk, or
// that runme.sh consults to size it, and which sit next to a locally built rootfs in
// dist/<arch>/current/installer. When a rootfs override is in use these are taken
// from that same build so that no part of an unrelated EVE build reaches the device.
// rootfs.img is handled separately (bind-mounted rather than copied), as is
// config.img (written per device by runme.sh) and firmware (not part of the disk).
var eveDiskPayloads = []string{"EFI", "boot", "eve_version", "eve_platform"}

// dirExists reports whether path exists and is a directory.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// buildLiveDiskFromEVEBuild assembles a live device disk out of a local EVE build,
// using EVE's mkimage-raw-efi package image as the builder. No lfedge/eve image is
// involved: every partition comes from bitsDir (a dist/<arch>/current/installer
// directory) and make-raw does the assembly, with assembleLiveDiskScript standing in
// for runme.sh, which only the lfedge/eve image carries.
//
// imageDirPath must already exist; its cleanup on failure is the caller's business.
func buildLiveDiskFromEVEBuild(ctx context.Context, log *logrus.Entry,
	imageDirPath, bitsDir, rootfsPath, builderImage string, config *api.EveConfig,
	proxyCACerts []*pem.Block, diskSize uint64) (imageSpec provider.ImageSpec, err error) {

	haveBuilder, err := utils.HaveDockerImage(ctx, log, builderImage)
	if err != nil {
		return imageSpec, fmt.Errorf("failed to check for disk builder image %q: %w",
			builderImage, err)
	}
	if !haveBuilder {
		if err = utils.PullDockerImage(ctx, log, builderImage); err != nil {
			return imageSpec, fmt.Errorf("failed to pull disk builder image %q: %w",
				builderImage, err)
		}
	}

	// Each device needs its own UEFI firmware: QEMU uses OVMF_VARS.fd as writable
	// pflash, so it cannot be shared between devices or with the build tree.
	imageSpec.UEFIFirmwareDirPath = filepath.Join(imageDirPath, "firmware")
	err = utils.CopyFolder(filepath.Join(bitsDir, "firmware"),
		imageSpec.UEFIFirmwareDirPath)
	if err != nil {
		return imageSpec, fmt.Errorf("failed to copy UEFI firmware from %q: %w",
			bitsDir, err)
	}

	// Stage what make-raw reads from /parts. config.img is copied because the script
	// mcopies this device's identity into it, and the rest so that nothing in the
	// build tree can be written to. rootfs.img is bind-mounted instead of copied: it
	// is by far the largest input and is only ever read.
	partsDir := filepath.Join(imageDirPath, "parts")
	for _, name := range []string{"EFI", "boot", "config.img"} {
		src := filepath.Join(bitsDir, name)
		srcInfo, statErr := os.Stat(src)
		if statErr != nil {
			// Only config.img and EFI are mandatory, and both were checked by
			// eveBuildHasDiskParts; make-raw treats a missing boot dir as optional.
			continue
		}
		dst := filepath.Join(partsDir, name)
		if srcInfo.IsDir() {
			err = utils.CopyFolder(src, dst)
		} else {
			err = utils.CopyFile(src, dst)
		}
		if err != nil {
			return imageSpec, fmt.Errorf("failed to stage %q for the disk build: %w",
				src, err)
		}
	}

	var configDir string
	configDir, err = makeEVEConfigDir(imageDirPath, config, proxyCACerts)
	if err != nil {
		return imageSpec, fmt.Errorf("failed to prepare EVE config dir: %w", err)
	}
	volumeMap := map[string]string{
		"/parts":            partsDir,
		"/parts/rootfs.img": rootfsPath,
		"/out":              imageDirPath,
	}
	if configDir != "" {
		volumeMap["/in"] = configDir
		defer os.RemoveAll(configDir)
	}

	diskSizeMB := uint64(defaultLiveDiskSizeMB)
	if diskSize != 0 {
		diskSizeMB = diskSize >> 20
	}
	script := fmt.Sprintf(assembleLiveDiskScript, diskSizeMB, liveDiskPartSpec)

	log.Infof("Assembling a %d MB device disk from EVE build %q using %s; "+
		"no EVE container image involved", diskSizeMB, bitsDir, builderImage)
	result, err := utils.RunDockerEntrypoint(ctx, log, builderImage,
		[]string{"/bin/sh"}, []string{"-c", script}, volumeMap, "")
	if err != nil {
		return imageSpec, fmt.Errorf("failed to assemble the device disk: %w", err)
	}
	log.Debugf("Disk build output:\n%s", result)

	rawPath := filepath.Join(imageDirPath, "live.raw")
	if _, err = os.Stat(rawPath); err != nil {
		log.Infof("Disk build output:\n%s", result)
		return imageSpec, fmt.Errorf("the disk build produced no %s: %w", rawPath, err)
	}

	// Compress into qcow2, matching what runme.sh's dump() would have produced, and
	// drop the sparse raw so only one copy of the disk is kept.
	imageSpec.Qcow2ImagePath = filepath.Join(imageDirPath, "live.raw.qcow2")
	convert := exec.CommandContext(ctx, "qemu-img", "convert", "-c",
		"-f", "raw", "-O", "qcow2", rawPath, imageSpec.Qcow2ImagePath)
	if out, cmdErr := convert.CombinedOutput(); cmdErr != nil {
		err = fmt.Errorf("failed to convert %q to qcow2: %w: %s",
			rawPath, cmdErr, string(out))
		return imageSpec, err
	}
	if rmErr := os.Remove(rawPath); rmErr != nil {
		log.Warnf("Failed to remove intermediate raw disk %q: %v", rawPath, rmErr)
	}

	log.Infof("Assembled device disk %q from EVE build %q",
		imageSpec.Qcow2ImagePath, bitsDir)
	return imageSpec, nil
}

// buildEVEImage builds an EVE image (QCOW2 or RAW) using EVE Docker image as the builder.
// It optionally extracts UEFI firmware, mounts configuration files, and invokes the
// EVE container to produce the final disk image.
//
// Parameters:
//   - ctx: Context for cancellation and timeouts.
//   - log: Logrus entry for structured logging.
//   - imageDirPath: Path to the output directory.
//   - dockerImageName: Name of the EVE Docker image to build from.
//   - rootfsOverride: Optional path to a locally built rootfs image to use instead of
//     the one baked into the EVE Docker image. The other artifacts of that build
//     (see eveDiskPayloads) and its UEFI firmware are then used as well, leaving the
//     Docker image to contribute only runme.sh, make-raw and the tools they need.
//   - diskBuilderImage: Optional mkimage-raw-efi package image. When it is set and the
//     build referenced by rootfsOverride is complete, the disk is assembled from that
//     build alone and dockerImageName is not used at all.
//   - config: Optional EveConfig providing server, certificates, keys, and JSON configs.
//   - proxyCACerts: Optional slice of PEM blocks containing trusted proxy CA certificates.
//   - installer: If true, builds a RAW installer image instead of the live QCOW2 image.
//
// Behavior:
//   - Validates rootfsOverride, which does not apply to installer images.
//   - Ensures the target directory exists.
//   - Obtains UEFI firmware (from the local build if given, else from the Docker
//     image) unless building an installer.
//   - Creates a temporary configuration directory with the contents of `config`.
//   - Runs the Docker container with appropriate args and mounts to generate the EVE image.
//   - Cleans up temporary configuration directory after execution.
//
// Returns an error if any step fails (directory creation, firmware extraction,
// Docker run, etc.).
func buildEVEImage(ctx context.Context, log *logrus.Entry,
	imageDirPath, dockerImageName, rootfsOverride, diskBuilderImage string,
	config *api.EveConfig, proxyCACerts []*pem.Block,
	diskSize uint64, installer bool) (imageSpec provider.ImageSpec, err error) {

	// Validate the rootfs override before doing any work. A locally built rootfs
	// lives in dist/<arch>/current/installer, which is exactly the /bits directory
	// of the EVE container image, so remember it as the source for the rest of the
	// disk's payloads as well.
	var eveBitsDir string
	if rootfsOverride != "" {
		if installer {
			log.Warnf("Ignoring EVE rootfs override %q: an installer image embeds "+
				"its own rootfs, so the installed EVE will come from the container "+
				"image %s", rootfsOverride, dockerImageName)
			rootfsOverride = ""
		} else {
			var rootfsInfo os.FileInfo
			if rootfsInfo, err = os.Stat(rootfsOverride); err != nil {
				err = fmt.Errorf("cannot use EVE rootfs override %q: %w",
					rootfsOverride, err)
				return imageSpec, err
			}
			if !rootfsInfo.Mode().IsRegular() {
				err = fmt.Errorf("EVE rootfs override %q is not a regular file",
					rootfsOverride)
				return imageSpec, err
			}
			eveBitsDir = eveBuildDirOf(rootfsOverride)
		}
	}

	// Ensure the target directory exists.
	if err = os.MkdirAll(imageDirPath, 0o755); err != nil {
		err = fmt.Errorf("failed to create EVE image directory %q: %w", imageDirPath, err)
		return imageSpec, err
	}
	defer func() {
		if err != nil {
			if removeErr := os.RemoveAll(imageDirPath); removeErr != nil {
				log.Warnf("Failed to remove EVE image directory %q : %v",
					imageDirPath, removeErr)
			}
		}
	}()

	// When the local build carries every partition, assemble the disk from it alone.
	// This keeps a pillar-only iteration down to "make ROOTFS_FORMAT=ext4 pkgs rootfs
	// diskparts" with no lfedge/eve image anywhere in the loop.
	if !installer && diskBuilderImage != "" && eveBuildHasDiskParts(eveBitsDir) {
		return buildLiveDiskFromEVEBuild(ctx, log, imageDirPath, eveBitsDir,
			rootfsOverride, diskBuilderImage, config, proxyCACerts, diskSize)
	}

	if !installer {
		imageSpec.Qcow2ImagePath = filepath.Join(imageDirPath, "live.raw.qcow2")
		// UEFI firmware for EVE. Every device needs its own copy because QEMU uses
		// OVMF_VARS.fd as writable pflash, so this is always a copy and never a
		// bind mount of the source. Prefer the local build when one was given.
		imageSpec.UEFIFirmwareDirPath = filepath.Join(imageDirPath, "firmware")
		localFirmware := filepath.Join(eveBitsDir, "firmware")
		if eveBitsDir != "" && dirExists(localFirmware) {
			err = utils.CopyFolder(localFirmware, imageSpec.UEFIFirmwareDirPath)
			if err != nil {
				err = fmt.Errorf("failed to copy UEFI firmware from %q: %w",
					localFirmware, err)
				return imageSpec, err
			}
		} else {
			err = utils.ExtractFromDockerImage(ctx, log,
				dockerImageName, imageDirPath, "/bits/firmware")
			if err != nil {
				err = fmt.Errorf("failed to extract UEFI firmware from EVE image %s: %w",
					dockerImageName, err)
				return imageSpec, err
			}
		}
	} else {
		imageSpec.RawImagePath = filepath.Join(imageDirPath, "installer.raw")
	}

	// Build the volume mapping: container target → host source.
	// The config dir is created under imageDirPath so that, when the broker
	// runs inside a container, the path also exists on the host (it's bind-
	// mounted at the same path) and can be passed to docker-out-of-docker.
	var configDir string
	configDir, err = makeEVEConfigDir(imageDirPath, config, proxyCACerts)
	if err != nil {
		err = fmt.Errorf("failed to prepare EVE config dir: %w", err)
		return imageSpec, err
	}
	volumeMap := map[string]string{
		"/out": imageDirPath,
	}
	if configDir != "" {
		volumeMap["/in"] = configDir
		defer os.RemoveAll(configDir)
	}

	// Take the disk's payloads from a local EVE build instead of from the container
	// image. The rootfs is large and read-only, so it is bind-mounted in place; the
	// small ones are staged into this device's own directory (under imageDirPath, so
	// that the path is also valid on the host for docker-out-of-docker) which keeps
	// them per-device and keeps the caller's build tree read-only in practice, even
	// though bind mounts here are writable. config.img is deliberately not staged:
	// runme.sh mcopies this device's identity into it, and the container image's
	// writable layer is the right place for that. What remains from the container
	// image is therefore runme.sh, make-raw and the tools they need.
	if rootfsOverride != "" {
		volumeMap["/bits/rootfs.img"] = rootfsOverride
		stagedBits := filepath.Join(imageDirPath, "bits")
		var staged []string
		for _, name := range eveDiskPayloads {
			src := filepath.Join(eveBitsDir, name)
			srcInfo, statErr := os.Stat(src)
			if statErr != nil {
				// Not every build produces every artifact; fall back to the
				// container image's copy for whatever is absent.
				continue
			}
			dst := filepath.Join(stagedBits, name)
			if srcInfo.IsDir() {
				err = utils.CopyFolder(src, dst)
			} else {
				err = utils.CopyFile(src, dst)
			}
			if err != nil {
				err = fmt.Errorf("failed to stage EVE build artifact %q: %w", src, err)
				return imageSpec, err
			}
			volumeMap["/bits/"+name] = dst
			staged = append(staged, name)
		}
		log.Infof("Assembling the device disk from EVE build %q (rootfs.img, %s); "+
			"container image %s contributes only the config partition and the tools "+
			"that build the disk", eveBitsDir, strings.Join(staged, ", "),
			dockerImageName)
	}

	// Run the EVE docker container to build the EVE disk image.
	cmd := "-f qcow2 live"
	if installer {
		cmd = "-f raw installer_raw"
	}
	if diskSize != 0 {
		diskSizeMB := diskSize >> 20
		cmd += fmt.Sprintf(" %d", diskSizeMB)
	}
	log.Infof("Building EVE image into the file %q", imageSpec.ImageFilePath())
	result, err := utils.RunDockerCommand(
		ctx, log, dockerImageName, cmd, volumeMap, "")
	if err != nil {
		err = fmt.Errorf("failed to run docker command for EVE image build: %w", err)
		return imageSpec, err
	}

	const maxOutputLen = 256
	truncateOutput := func(output string) string {
		if len(output) <= maxOutputLen {
			return output
		}
		return output[:maxOutputLen] + "…"
	}

	// Check that the generated image file exists and is non-empty
	info, statErr := os.Stat(imageSpec.ImageFilePath())
	truncatedResult := truncateOutput(result)

	if statErr != nil {
		log.Infof("Docker output (truncated to %d chars):\n%s",
			maxOutputLen, truncatedResult)
		err = fmt.Errorf("expected EVE image file %q not found: %w",
			imageSpec.ImageFilePath(), statErr)
		return imageSpec, err
	}
	if info.Size() == 0 {
		log.Infof("Docker output (truncated to %d chars):\n%s",
			maxOutputLen, truncatedResult)
		err = fmt.Errorf("EVE image file %q is empty", imageSpec.ImageFilePath())
		return imageSpec, err
	}

	log.Infof("Successfully built EVE image from %s: %s",
		dockerImageName, imageSpec.ImageFilePath())
	log.Debugf("Docker output:\n%s", result)
	return imageSpec, nil
}

// makeEVEConfigDir creates a temporary directory containing EVE configuration
// files derived from the provided EveConfig. Each non-empty field is written
// into a specific file under the directory structure expected by EVE.
//
// The directory is created under parentDir so that, when the broker runs
// inside a container, the path also exists on the host and can be bind-mounted
// into a docker-out-of-docker container.
//
// Certificates are validated before writing. Proxy CA certificates passed in
// proxyCACerts are appended to v2tlsbaseroot-certificates.pem.
func makeEVEConfigDir(parentDir string,
	config *api.EveConfig, proxyCACerts []*pem.Block) (dirPath string, err error) {

	if config == nil && len(proxyCACerts) == 0 {
		return "", nil
	}

	dirPath, err = os.MkdirTemp(parentDir, "eve-config-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temporary config directory: %w", err)
	}
	// Ensure cleanup on error
	defer func() {
		if err != nil {
			os.RemoveAll(dirPath)
		}
	}()

	// Helper to write a file only if data is non-empty
	writeFile := func(relPath string, data []byte) error {
		if len(data) == 0 {
			return nil
		}
		fullPath := filepath.Join(dirPath, relPath)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			return fmt.Errorf("failed to create directory for %q: %w", fullPath, err)
		}
		if err := os.WriteFile(fullPath, data, 0o600); err != nil {
			return fmt.Errorf("failed to write file %q: %w", fullPath, err)
		}
		return nil
	}

	err = writeFile("server", []byte(config.ServerName))
	if err != nil {
		return "", err
	}
	err = writeFile("soft_serial", []byte(config.SoftSerial))
	if err != nil {
		return "", err
	}

	if len(config.OnboardCertPem) > 0 {
		_, err = utils.ValidatePEMCerts([]byte(config.OnboardCertPem), true)
		if err != nil {
			return "", fmt.Errorf("onboard certificate invalid: %w", err)
		}
		err = writeFile("onboard.cert.pem", []byte(config.OnboardCertPem))
		if err != nil {
			return "", err
		}
	}
	if len(config.OnboardKeyPem) > 0 {
		err = utils.ValidatePEMPrivateKeyECDSA([]byte(config.OnboardKeyPem))
		if err != nil {
			return "", fmt.Errorf("onboard key invalid: %w", err)
		}
		err = writeFile("onboard.key.pem", []byte(config.OnboardKeyPem))
		if err != nil {
			return "", err
		}
	}

	if len(config.RootCertPem) > 0 {
		_, err = utils.ValidatePEMCerts([]byte(config.RootCertPem), true)
		if err != nil {
			return "", fmt.Errorf("root certificate invalid: %w", err)
		}
		err = writeFile("root-certificate.pem", []byte(config.RootCertPem))
		if err != nil {
			return "", err
		}
	}

	// Handle V2 TLS certs and append proxy CA certs
	var certDataBuilder strings.Builder
	writeV2TLS := false

	// Validate and append V2TlsCertsPem
	for _, pemStr := range config.V2TlsCertsPem {
		_, err = utils.ValidatePEMCerts([]byte(pemStr), true)
		if err != nil {
			return "", fmt.Errorf("v2 TLS certificate invalid: %w", err)
		}
		certDataBuilder.WriteString(pemStr)
		if !strings.HasSuffix(pemStr, "\n") {
			certDataBuilder.WriteString("\n")
		}
		writeV2TLS = true
	}

	// Append validated proxy CA certificates
	for _, block := range proxyCACerts {
		writeV2TLS = true
		certPEM := pem.EncodeToMemory(block)
		certDataBuilder.Write(certPEM)
		if len(certPEM) > 0 && certPEM[len(certPEM)-1] != '\n' {
			certDataBuilder.WriteString("\n")
		}
	}

	if writeV2TLS {
		certData := []byte(certDataBuilder.String())
		err = writeFile("v2tlsbaseroot-certificates.pem", certData)
		if err != nil {
			return "", err
		}
	}

	if len(config.SshKeys) > 0 {
		keysData := strings.Join(config.SshKeys, "\n")
		err = writeFile("authorized_keys", []byte(keysData))
		if err != nil {
			return "", err
		}
	}

	if len(config.GrubOptions) > 0 {
		grubConfig := strings.Join(config.GrubOptions, "\n")
		err = writeFile("grub.cfg", []byte(grubConfig))
		if err != nil {
			return "", err
		}
	}

	err = writeFile("GlobalConfig/global.json", []byte(config.GlobalJson))
	if err != nil {
		return "", err
	}
	err = writeFile("DevicePortConfig/override.json", []byte(config.OverrideJson))
	if err != nil {
		return "", err
	}
	if len(config.BootstrapConfigPb) > 0 {
		err = writeFile("bootstrap-config.pb", config.BootstrapConfigPb)
		if err != nil {
			return "", err
		}
	}

	return dirPath, nil
}
