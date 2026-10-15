package imageservice

import "image"

// normalizeGreenScreenBackground flattens only an opaque green background
// connected to the image border. Enclosed green details and alpha are retained.
func normalizeGreenScreenBackground(img *image.NRGBA) int {
	if img == nil || img.Bounds().Empty() {
		return 0
	}
	width, height := img.Bounds().Dx(), img.Bounds().Dy()
	green := func(offset int) bool {
		r, g, b := int(img.Pix[offset]), int(img.Pix[offset+1]), int(img.Pix[offset+2])
		return img.Pix[offset+3] == 255 && g >= 80 && g-r >= 40 && g-b >= 40
	}
	border := func(visit func(int, int)) {
		for x := 0; x < width; x++ {
			visit(x, 0)
			if height > 1 {
				visit(x, height-1)
			}
		}
		for y := 1; y < height-1; y++ {
			visit(0, y)
			if width > 1 {
				visit(width-1, y)
			}
		}
	}
	var histogram [3][256]int
	borderCount, greenCount := 0, 0
	border(func(x, y int) {
		borderCount++
		offset := y*img.Stride + x*4
		if !green(offset) {
			return
		}
		greenCount++
		for channel := range histogram {
			histogram[channel][img.Pix[offset+channel]]++
		}
	})
	if greenCount*10 < borderCount*9 {
		return 0
	}
	var key [3]int
	for channel := range histogram {
		count := 0
		for value, frequency := range histogram[channel] {
			count += frequency
			if count > (greenCount-1)/2 {
				key[channel] = value
				break
			}
		}
	}
	matches := func(offset int) bool {
		if !green(offset) {
			return false
		}
		for channel, value := range key {
			difference := int(img.Pix[offset+channel]) - value
			if difference < -64 || difference > 64 {
				return false
			}
		}
		return true
	}
	matchingBorder := 0
	border(func(x, y int) {
		if matches(y*img.Stride + x*4) {
			matchingBorder++
		}
	})
	if matchingBorder*10 < borderCount*9 {
		return 0
	}

	visited := make([]bool, width*height)
	pending := make([]int32, 0, borderCount)
	add := func(x, y int) {
		index := y*width + x
		if visited[index] || !matches(y*img.Stride+x*4) {
			return
		}
		visited[index] = true
		pending = append(pending, int32(index))
	}
	border(add)
	changed := 0
	for len(pending) > 0 {
		index := int(pending[len(pending)-1])
		pending = pending[:len(pending)-1]
		x, y := index%width, index/width
		offset := y*img.Stride + x*4
		if img.Pix[offset] != 0 || img.Pix[offset+1] != 255 || img.Pix[offset+2] != 0 {
			img.Pix[offset], img.Pix[offset+1], img.Pix[offset+2] = 0, 255, 0
			changed++
		}
		if x > 0 {
			add(x-1, y)
		}
		if x+1 < width {
			add(x+1, y)
		}
		if y > 0 {
			add(x, y-1)
		}
		if y+1 < height {
			add(x, y+1)
		}
	}
	return changed
}
