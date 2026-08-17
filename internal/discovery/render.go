package discovery

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// maxImageBytes guards the API's per-image size limit (5MB) with headroom.
const maxImageBytes = 4 << 20

// RenderPages rasterizes pages first..last of the PDF into cacheDir as JPEGs
// (one per page) by shelling out to pdftoppm, and returns pdf page -> file
// path. The scans have no text layer at all — pdftotext yields only the print
// shop's watermarks — so rendering for a vision model is the only way in.
// JPEG rather than PNG because these are photographic scans; at the same DPI
// the JPEG is a fraction of the size, which matters for the request payload.
// Already-rendered pages are reused, so re-runs cost nothing.
func RenderPages(pdfPath, cacheDir string, first, last, dpi int) (map[int]string, error) {
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return nil, err
	}
	pages := make(map[int]string)
	for p := first; p <= last; p++ {
		out := filepath.Join(cacheDir, fmt.Sprintf("page-%02d.jpg", p))
		if _, err := os.Stat(out); err != nil {
			// pdftoppm writes <root>-NN.jpg; render one page at a time so the
			// filename is fully under our control.
			root := filepath.Join(cacheDir, fmt.Sprintf("page-%02d", p))
			cmd := exec.Command("pdftoppm", "-jpeg", "-jpegopt", "quality=85",
				"-r", fmt.Sprint(dpi), "-f", fmt.Sprint(p), "-l", fmt.Sprint(p), pdfPath, root)
			if msg, err := cmd.CombinedOutput(); err != nil {
				return nil, fmt.Errorf("pdftoppm page %d: %v: %s", p, err, msg)
			}
			// pdftoppm appends its own page suffix (page-05-05.jpg or
			// page-05-5.jpg depending on version); rename to our stable name.
			matches, _ := filepath.Glob(root + "-*.jpg")
			if len(matches) != 1 {
				return nil, fmt.Errorf("pdftoppm page %d: expected 1 output file, got %v", p, matches)
			}
			if err := os.Rename(matches[0], out); err != nil {
				return nil, err
			}
		}
		info, err := os.Stat(out)
		if err != nil {
			return nil, err
		}
		if info.Size() > maxImageBytes {
			return nil, fmt.Errorf("page %d render is %dMB, over the API image limit — lower -dpi", p, info.Size()>>20)
		}
		pages[p] = out
	}
	return pages, nil
}
