/*
Copyright © 2021 blacktop

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in
all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
THE SOFTWARE.
*/
package cmd

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/apex/log"
	"github.com/blacktop/go-macho"
	"github.com/blacktop/ipsw/pkg/dyld"
	"github.com/fullsailor/pkcs7"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

func init() {
	dyldCmd.AddCommand(dyldInfoCmd)

	dyldInfoCmd.Flags().BoolP("dylibs", "l", false, "List dylibs and their versions")
	dyldInfoCmd.Flags().BoolP("sig", "s", false, "Print code signature")
	dyldInfoCmd.MarkZshCompPositionalArgumentFile(1, "dyld_shared_cache*")
}

// infoCmd represents the info command
var dyldInfoCmd = &cobra.Command{
	Use:   "info [options] <dyld_shared_cache>",
	Short: "Parse dyld_shared_cache",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if Verbose {
			log.SetLevel(log.DebugLevel)
		}

		// showHeader, _ := cmd.Flags().GetBool("header")
		showDylibs, _ := cmd.Flags().GetBool("dylibs")
		showSignature, _ := cmd.Flags().GetBool("sig")

		fileInfo, err := os.Lstat(args[0])
		if err != nil {
			return fmt.Errorf("file %s does not exist", args[0])
		}

		dyldFile := args[0]

		// Check if file is a symlink
		if fileInfo.Mode()&os.ModeSymlink != 0 {
			dyldFile, err = os.Readlink(args[0])
			if err != nil {
				return errors.Wrapf(err, "failed to read symlink %s", args[0])
			}
			// TODO: this seems like it would break
			linkParent := filepath.Dir(args[0])
			linkRoot := filepath.Dir(linkParent)

			dyldFile = filepath.Join(linkRoot, dyldFile)
		}

		// TODO: check for
		// if ( dylibInfo->isAlias )
		//   	printf("[alias] %s\n", dylibInfo->path);

		f, err := dyld.Open(dyldFile)
		if err != nil {
			return err
		}
		defer f.Close()

		// if showHeader {
		fmt.Println(f)
		// }

		if showSignature {
			fmt.Println("Code Signature")
			fmt.Println("==============")
			if f.CodeSignature != nil {
				cds := f.CodeSignature.CodeDirectories
				if len(cds) > 0 {
					for _, cd := range cds {
						fmt.Printf("Code Directory (%d bytes)\n", cd.Header.Length)
						fmt.Printf("\tVersion:     %s\n"+
							"\tFlags:       %s\n"+
							"\tCodeLimit:   0x%x\n"+
							"\tIdentifier:  %s (@0x%x)\n"+
							"\tTeamID:      %s\n"+
							"\tCDHash:      %s (computed)\n"+
							"\t# of hashes: %d code (%d pages) + %d special\n"+
							"\tHashes @%d size: %d Type: %s\n",
							cd.Header.Version,
							cd.Header.Flags,
							cd.Header.CodeLimit,
							cd.ID,
							cd.Header.IdentOffset,
							cd.TeamID,
							cd.CDHash,
							cd.Header.NCodeSlots,
							int(math.Pow(2, float64(cd.Header.PageSize))),
							cd.Header.NSpecialSlots,
							cd.Header.HashOffset,
							cd.Header.HashSize,
							cd.Header.HashType)
						if Verbose {
							for _, sslot := range cd.SpecialSlots {
								fmt.Printf("\t\t%s\n", sslot.Desc)
							}
							for _, cslot := range cd.CodeSlots {
								fmt.Printf("\t\t%s\n", cslot.Desc)
							}
						}
					}
				}
				reqs := f.CodeSignature.Requirements
				if len(reqs) > 0 {
					fmt.Printf("Requirement Set (%d bytes) with %d requirement\n",
						reqs[0].Length, // TODO: fix this (needs to be length - sizeof(header))
						len(reqs))
					for idx, req := range reqs {
						fmt.Printf("\t%d: %s (@%d, %d bytes): %s\n",
							idx,
							req.Type,
							req.Offset,
							req.Length,
							req.Detail)
					}
				}
				if len(f.CodeSignature.CMSSignature) > 0 {
					fmt.Println("CMS (RFC3852) signature:")
					p7, err := pkcs7.Parse(f.CodeSignature.CMSSignature)
					if err != nil {
						return err
					}
					w := tabwriter.NewWriter(os.Stdout, 0, 0, 1, ' ', 0)
					for _, cert := range p7.Certificates {
						var ou string
						if cert.Issuer.Organization != nil {
							ou = cert.Issuer.Organization[0]
						}
						if cert.Issuer.OrganizationalUnit != nil {
							ou = cert.Issuer.OrganizationalUnit[0]
						}
						fmt.Fprintf(w, "        OU: %s\tCN: %s\t(%s thru %s)\n",
							ou,
							cert.Subject.CommonName,
							cert.NotBefore.Format("2006-01-02"),
							cert.NotAfter.Format("2006-01-02"))
					}
					w.Flush()
				}
			} else {
				fmt.Println("  - no code signature data")
			}
			fmt.Println()
		}

		if showDylibs {
			fmt.Println("Images")
			fmt.Println("======")
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 1, ' ', tabwriter.DiscardEmptyColumns)
			for idx, img := range f.Images {
				if f.FormatVersion.IsDylibsExpectedOnDisk() {
					m, err := macho.Open(img.Name)
					if err != nil {
						if serr, ok := err.(*macho.FormatError); !ok {
							return errors.Wrapf(serr, "failed to open MachO %s", img.Name)
						}
						fat, err := macho.OpenFat(img.Name)
						if err != nil {
							return errors.Wrapf(err, "failed to open Fat MachO %s", img.Name)
						}
						fmt.Fprintf(w, "%4d:\t0x%0X\t(%s)\t%s\n", idx+1, img.Info.Address, fat.Arches[0].DylibID().CurrentVersion, img.Name)
						fat.Close()
						continue
					}
					fmt.Fprintf(w, "%4d:\t0x%0X\t(%s)\t%s\n", idx+1, img.Info.Address, m.DylibID().CurrentVersion, img.Name)
					m.Close()
				} else {
					m, err := img.GetPartialMacho()
					if err != nil {
						return errors.Wrap(err, "failed to create MachO")
					}
					fmt.Fprintf(w, "%4d:\t0x%0X\t%s\t(%s)\n", idx+1, img.Info.Address, img.Name, m.DylibID().CurrentVersion)
					m.Close()
				}
				// w.Flush()
			}
			w.Flush()
		}
		return nil
	},
}
