// 一次性工具：生成托盘图标 assets/icon.ico。
// 需要重新生成时在仓库根目录执行:  go run ./tools/genicon
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
)

const S = 256

var (
	bg     = color.RGBA{R: 18, G: 21, B: 27, A: 255}      // 深底
	keyHi  = color.RGBA{R: 216, G: 222, B: 232, A: 235}   // 亮键帽
	keyLo  = color.RGBA{R: 216, G: 222, B: 232, A: 130}   // 暗键帽
	accent = color.RGBA{R: 79, G: 140, B: 255, A: 255}    // 主题蓝
)

func fillRect(img *image.RGBA, x0, y0, w, h int, c color.RGBA) {
	for y := y0; y < y0+h && y < S; y++ {
		for x := x0; x < x0+w && x < S; x++ {
			img.SetRGBA(x, y, c)
		}
	}
}

func main() {
	out := "assets/icon.ico"
	if len(os.Args) > 1 {
		out = os.Args[1]
	}

	img := image.NewRGBA(image.Rect(0, 0, S, S))
	fillRect(img, 0, 0, S, S, bg)

	const cols = 5
	const size, gap = 36, 10
	startX := (S - cols*size - (cols-1)*gap) / 2
	for rowIdx, y := range []int{38, 84, 130} {
		for i := range cols {
			c := keyHi
			if (rowIdx+i)%2 == 1 {
				c = keyLo
			}
			fillRect(img, startX+i*(size+gap), y, size, size, c)
		}
	}
	// 底部一长条“空格键”用主题色点亮
	fillRect(img, startX, 178, 3*size+2*gap, 34, accent)
	fillRect(img, startX+3*(size+gap), 178, 2*size+gap, 34, keyLo)

	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		panic(err)
	}

	var f *os.File
	var err error
	f, err = os.Create(out)
	if err != nil {
		panic(err)
	}
	defer f.Close()

	// ICO 容器：文件头 + 单个目录项 + 内嵌 PNG（Vista 起支持）
	if err := binary.Write(f, binary.LittleEndian,
		struct{ Reserved, Type, Count uint16 }{0, 1, 1}); err != nil {
		panic(err)
	}
	entry := struct {
		Width, Height, Colors, Reserved byte
		Planes, Bpp                     uint16
		Size, Offset                    uint32
	}{0, 0, 0, 0, 1, 32, uint32(pngBuf.Len()), 22}
	if err := binary.Write(f, binary.LittleEndian, entry); err != nil {
		panic(err)
	}
	if _, err := f.Write(pngBuf.Bytes()); err != nil {
		panic(err)
	}
	fmt.Println("已生成", out)
}
