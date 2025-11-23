package main

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"log"
	"os"
	"path/filepath"

	"github.com/dhowden/tag"
)

func main() {
	if len(os.Args) != 2 {
		log.Fatalf("用法: %s <目录路径>", os.Args[0])
	}

	dirPath := os.Args[1]

	// 读取目录中的所有文件
	files, err := os.ReadDir(dirPath)
	if err != nil {
		log.Fatalf("读取目录失败: %v", err)
	}

	flacCount := 0
	artCount := 0

	for _, file := range files {
		if filepath.Ext(file.Name()) == ".flac" {
			flacCount++
			flacPath := filepath.Join(dirPath, file.Name())

			// 提取专辑封面信息
			if err := extractAlbumArtInfo(flacPath); err != nil {
				fmt.Printf("文件 %s: 无专辑封面\n", file.Name())
			} else {
				artCount++
			}
		}
	}

	fmt.Printf("\n统计信息:\n")
	fmt.Printf("总FLAC文件数: %d\n", flacCount)
	fmt.Printf("包含专辑封面的文件数: %d\n", artCount)
	fmt.Printf("无专辑封面的文件数: %d\n", flacCount-artCount)
}

func extractAlbumArtInfo(flacPath string) error {
	f, err := os.Open(flacPath)
	if err != nil {
		return fmt.Errorf("打开文件失败: %v", err)
	}
	defer f.Close()

	m, err := tag.ReadFrom(f)
	if err != nil {
		return fmt.Errorf("读取标签失败: %v", err)
	}

	// 获取歌曲信息
	title := m.Title()
	if title == "" {
		title = "未知"
	}
	artist := m.Artist()
	if artist == "" {
		artist = "未知"
	}
	album := m.Album()
	if album == "" {
		album = "未知"
	}

	// 获取专辑封面
	if pic := m.Picture(); pic != nil {
		img, format, err := image.Decode(bytes.NewReader(pic.Data))
		if err != nil {
			return fmt.Errorf("解码图片失败: %v", err)
		}

		bounds := img.Bounds()
		width := bounds.Dx()
		height := bounds.Dy()

		fmt.Printf("\n文件: %s\n", filepath.Base(flacPath))
		fmt.Printf("  标题: %s\n", title)
		fmt.Printf("  艺术家: %s\n", artist)
		fmt.Printf("  专辑: %s\n", album)
		fmt.Printf("  图片格式: %s\n", format)
		fmt.Printf("  分辨率: %d x %d 像素\n", width, height)
		fmt.Printf("  图片大小: %d 字节\n", len(pic.Data))

		// 显示图片类型信息
		if pic.MIMEType != "" {
			fmt.Printf("  MIME类型: %s\n", pic.MIMEType)
		}
		if pic.Description != "" {
			fmt.Printf("  描述: %s\n", pic.Description)
		}

		return nil
	}

	return fmt.Errorf("无专辑封面")
}
