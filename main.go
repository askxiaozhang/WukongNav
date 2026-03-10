package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// --- Data Models (V3) ---
type Category struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

type Tag struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type Item struct {
	ID         int    `json:"id"`
	CategoryID int    `json:"category_id"`
	Title      string `json:"title"`
	Desc       string `json:"desc"`
	URL        string `json:"url"`
	CreatorIP  string `json:"creator_ip"`
	LastEditor string `json:"last_editor"`
	TagIDs     []int  `json:"tag_ids"`
	Status     string `json:"status"`
}

type NavData struct {
	Categories []Category `json:"categories"`
	Tags       []Tag      `json:"tags"`
	Items      []Item     `json:"items"`
}

// Global state
var (
	navData    NavData
	dataMutex  sync.RWMutex
	dataFile   = "static/nav_data.json"
	allowedIPs = map[string]bool{
		"127.0.0.1":       true,
		"192.168.110.88":  true,
		"192.168.110.101": true,
		"192.168.110.109": true,
		"192.168.110.43":  true,
		"192.168.110.86":  true,
	}
)

func getDefaultData() NavData {
	return NavData{
		Categories: []Category{
			{ID: 1, Name: "报告与文档", Color: "green"},
			{ID: 2, Name: "项目管理", Color: "blue"},
			{ID: 3, Name: "数据标注与管理", Color: "orange"},
			{ID: 4, Name: "AI与开发工具", Color: "purple"},
			{ID: 5, Name: "系统监控与订阅", Color: "red"},
		},
		Tags: []Tag{
			{ID: 1, Name: "常用"},
			{ID: 2, Name: "管理后台"},
			{ID: 3, Name: "测试环境"},
		},
		Items: []Item{},
	}
}

func loadData() {
	dataMutex.Lock()
	defer dataMutex.Unlock()

	data, err := ioutil.ReadFile(dataFile)
	if err != nil {
		if os.IsNotExist(err) {
			navData = getDefaultData()
			saveDataInternal()
			return
		}
		log.Printf("Error reading data file: %v\n", err)
		return
	}

	var d NavData
	if err := json.Unmarshal(data, &d); err != nil {
		log.Printf("Error parsing data: %v\n", err)
		navData = getDefaultData()
		return
	}

	// Data Migration Hooks
	if len(d.Tags) == 0 {
		d.Tags = getDefaultData().Tags
	}
	for i := range d.Items {
		if d.Items[i].TagIDs == nil {
			d.Items[i].TagIDs = []int{}
		}
	}
	navData = d
}

// saveDataInternal MUST be called with dataMutex.Lock() held!
func saveDataInternal() {
	data, err := json.MarshalIndent(navData, "", "  ")
	if err != nil {
		log.Printf("Error marshaling data: %v\n", err)
		return
	}

	// Ensure static directory exists
	os.MkdirAll(filepath.Dir(dataFile), 0755)

	if err := ioutil.WriteFile(dataFile, data, 0644); err != nil {
		log.Printf("Error saving data: %v\n", err)
	}
}

func getClientIP(c *gin.Context) string {
	ip := c.ClientIP()
	if ip == "::1" {
		return "127.0.0.1"
	}
	return ip
}

// --- Middleware ---
func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		clientIP := getClientIP(c)
		if !allowedIPs[clientIP] {
			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"message": fmt.Sprintf("拒绝访问: 您的IP (%s) 没有修改权限。", clientIP),
			})
			c.Abort()
			return
		}
		c.Next()
	}
}

// Helper to get local IP for printing
func getLocalIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "127.0.0.1"
	}
	defer conn.Close()
	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP.String()
}

func main() {
	// Initialize directories
	os.MkdirAll("static", 0755)
	os.MkdirAll("uploads", 0755)
	os.MkdirAll("user_pages", 0755)
	loadData()

	r := gin.Default()

	// Serve static and uploads
	r.Static("/static", "./static")
	r.Static("/uploads", "./uploads")
	r.Static("/v", "./user_pages")
	r.LoadHTMLGlob("templates/*.html")

	// --- Public Routes ---
	r.GET("/", func(c *gin.Context) {
		clientIP := getClientIP(c)
		isAllowed := allowedIPs[clientIP]
		c.HTML(http.StatusOK, "index.html", gin.H{
			"client_ip":  clientIP,
			"is_allowed": isAllowed,
		})
	})

	r.GET("/api/data", func(c *gin.Context) {
		dataMutex.RLock()
		defer dataMutex.RUnlock()
		c.JSON(http.StatusOK, navData)
	})

	api := r.Group("/api")
	api.Use(AuthMiddleware())
	{
		// --- Tag API ---
		api.POST("/tag/add", func(c *gin.Context) {
			var req struct {
				Name string `json:"name"`
			}
			if err := c.ShouldBindJSON(&req); err != nil || req.Name == "" {
				c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "标签名不能为空"})
				return
			}

			dataMutex.Lock()
			defer dataMutex.Unlock()

			// Check exists
			for _, t := range navData.Tags {
				if t.Name == req.Name {
					c.JSON(http.StatusOK, gin.H{"success": true, "tag": t})
					return
				}
			}

			newID := 1
			for _, t := range navData.Tags {
				if t.ID >= newID {
					newID = t.ID + 1
				}
			}

			newTag := Tag{ID: newID, Name: req.Name}
			navData.Tags = append(navData.Tags, newTag)
			saveDataInternal()

			c.JSON(http.StatusOK, gin.H{"success": true, "tag": newTag})
		})

		// --- Category API ---
		api.POST("/category/add", func(c *gin.Context) {
			var req struct {
				Name  string `json:"name"`
				Color string `json:"color"`
			}
			if err := c.ShouldBindJSON(&req); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"success": false})
				return
			}
			if req.Color == "" {
				req.Color = "blue"
			}

			dataMutex.Lock()
			defer dataMutex.Unlock()

			newID := 1
			for _, cat := range navData.Categories {
				if cat.ID >= newID {
					newID = cat.ID + 1
				}
			}

			navData.Categories = append(navData.Categories, Category{
				ID:    newID,
				Name:  req.Name,
				Color: req.Color,
			})
			saveDataInternal()
			c.JSON(http.StatusOK, gin.H{"success": true})
		})

		api.POST("/category/delete", func(c *gin.Context) {
			var req struct {
				ID int `json:"id"`
			}
			if err := c.ShouldBindJSON(&req); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"success": false})
				return
			}

			dataMutex.Lock()
			defer dataMutex.Unlock()

			// Remove category
			var newCats []Category
			for _, cat := range navData.Categories {
				if cat.ID != req.ID {
					newCats = append(newCats, cat)
				}
			}
			navData.Categories = newCats

			// Remove items belonging to category
			var newItems []Item
			for _, item := range navData.Items {
				if item.CategoryID != req.ID {
					newItems = append(newItems, item)
				}
			}
			navData.Items = newItems

			saveDataInternal()
			c.JSON(http.StatusOK, gin.H{"success": true})
		})

		// --- Item API ---
		api.POST("/item/add", func(c *gin.Context) {
			var item Item
			contentType := c.GetHeader("Content-Type")

			if strings.Contains(contentType, "multipart/form-data") {
				// Handle Multipart (Upload/Paste)
				title := c.PostForm("title")
				desc := c.PostForm("desc")
				catID, _ := strconv.Atoi(c.PostForm("category_id"))
				tagIDsStr := c.PostFormArray("tag_ids[]")
				var tagIDs []int
				for _, s := range tagIDsStr {
					if id, err := strconv.Atoi(s); err == nil {
						tagIDs = append(tagIDs, id)
					}
				}

				sourceType := c.PostForm("source_type") // link, upload, paste
				url := c.PostForm("url")

				if sourceType == "upload" {
					file, err := c.FormFile("file")
					if err == nil {
						filename := fmt.Sprintf("%d_%s", time.Now().Unix(), file.Filename)
						savePath := filepath.Join("user_pages", filename)
						if err := c.SaveUploadedFile(file, savePath); err == nil {
							url = "/v/" + filename
						}
					}
				} else if sourceType == "paste" {
					content := c.PostForm("content")
					if content != "" {
						filename := fmt.Sprintf("pasted_%d.html", time.Now().Unix())
						savePath := filepath.Join("user_pages", filename)
						if err := ioutil.WriteFile(savePath, []byte(content), 0644); err == nil {
							url = "/v/" + filename
						}
					}
				}

				item = Item{
					Title:      title,
					Desc:       desc,
					CategoryID: catID,
					URL:        url,
					TagIDs:     tagIDs,
					Status:     "normal",
				}
			} else {
				if err := c.ShouldBindJSON(&item); err != nil {
					c.JSON(http.StatusBadRequest, gin.H{"success": false})
					return
				}
			}

			dataMutex.Lock()
			defer dataMutex.Unlock()

			newID := 1
			for _, i := range navData.Items {
				if i.ID >= newID {
					newID = i.ID + 1
				}
			}

			item.ID = newID
			item.CreatorIP = getClientIP(c)
			item.LastEditor = item.CreatorIP
			if item.TagIDs == nil {
				item.TagIDs = []int{}
			}

			navData.Items = append(navData.Items, item)
			saveDataInternal()
			c.JSON(http.StatusOK, gin.H{"success": true})
		})

		api.POST("/item/edit", func(c *gin.Context) {
			var req Item
			contentType := c.GetHeader("Content-Type")

			var sourceType, content string
			var file *multipart.FileHeader

			if strings.Contains(contentType, "multipart/form-data") {
				id, _ := strconv.Atoi(c.PostForm("id"))
				req.ID = id
				req.Title = c.PostForm("title")
				req.Desc = c.PostForm("desc")
				req.URL = c.PostForm("url")
				req.CategoryID, _ = strconv.Atoi(c.PostForm("category_id"))
				tagIDsStr := c.PostFormArray("tag_ids[]")
				for _, s := range tagIDsStr {
					if tid, err := strconv.Atoi(s); err == nil {
						req.TagIDs = append(req.TagIDs, tid)
					}
				}
				sourceType = c.PostForm("source_type")
				content = c.PostForm("content")
				file, _ = c.FormFile("file")
			} else {
				if err := c.ShouldBindJSON(&req); err != nil {
					c.JSON(http.StatusBadRequest, gin.H{"success": false})
					return
				}
			}

			dataMutex.Lock()
			defer dataMutex.Unlock()

			found := false
			for i, item := range navData.Items {
				if item.ID == req.ID {
					navData.Items[i].Title = req.Title
					navData.Items[i].Desc = req.Desc
					navData.Items[i].CategoryID = req.CategoryID
					if req.TagIDs != nil {
						navData.Items[i].TagIDs = req.TagIDs
					}

					// Handle URL updates based on source type during edit
					finalURL := req.URL
					if sourceType == "upload" && file != nil {
						filename := fmt.Sprintf("%d_%s", time.Now().Unix(), file.Filename)
						savePath := filepath.Join("user_pages", filename)
						if err := c.SaveUploadedFile(file, savePath); err == nil {
							finalURL = "/v/" + filename
						}
					} else if sourceType == "paste" && content != "" {
						filename := fmt.Sprintf("pasted_%d.html", time.Now().Unix())
						savePath := filepath.Join("user_pages", filename)
						if err := ioutil.WriteFile(savePath, []byte(content), 0644); err == nil {
							finalURL = "/v/" + filename
						}
					}
					navData.Items[i].URL = finalURL
					navData.Items[i].LastEditor = getClientIP(c)
					found = true
					break
				}
			}

			if found {
				saveDataInternal()
				c.JSON(http.StatusOK, gin.H{"success": true})
			} else {
				c.JSON(http.StatusNotFound, gin.H{"success": false})
			}
		})

		api.POST("/item/delete", func(c *gin.Context) {
			var req struct {
				ID int `json:"id"`
			}
			if err := c.ShouldBindJSON(&req); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"success": false})
				return
			}

			dataMutex.Lock()
			defer dataMutex.Unlock()

			var newItems []Item
			for _, item := range navData.Items {
				if item.ID != req.ID {
					newItems = append(newItems, item)
				}
			}
			navData.Items = newItems
			saveDataInternal()
			c.JSON(http.StatusOK, gin.H{"success": true})
		})
	}

	// --- Legacy Form POST Endpoints (Upload & Paste) ---
	// We handle these outside of `/api` mapping them directly for html form postings
	// and wrapping them in standard NavV3 Items logic

	// Helper to ensure an "Uploads" category exists and get its ID
	getUploadCategoryID := func() int {
		dataMutex.Lock()
		defer dataMutex.Unlock()
		uploadCatName := "上传文件"

		for _, cat := range navData.Categories {
			if cat.Name == uploadCatName {
				return cat.ID
			}
		}

		newID := 1
		for _, cat := range navData.Categories {
			if cat.ID >= newID {
				newID = cat.ID + 1
			}
		}
		navData.Categories = append(navData.Categories, Category{
			ID:    newID,
			Name:  uploadCatName,
			Color: "purple",
		})
		saveDataInternal()
		return newID
	}

	r.POST("/upload", AuthMiddleware(), func(c *gin.Context) {
		title := c.PostForm("title")
		file, err := c.FormFile("file")

		if err != nil || title == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Title and file are required"})
			return
		}

		ext := filepath.Ext(file.Filename)
		if ext != ".html" && ext != ".htm" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Only HTML files are allowed"})
			return
		}

		filename := fmt.Sprintf("%d_%s", time.Now().Unix(), file.Filename)
		uploadPath := filepath.Join("uploads", filename)

		if err := c.SaveUploadedFile(file, uploadPath); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save file"})
			return
		}

		catID := getUploadCategoryID()

		dataMutex.Lock()
		newID := 1
		for _, item := range navData.Items {
			if item.ID >= newID {
				newID = item.ID + 1
			}
		}
		newItem := Item{
			ID:         newID,
			CategoryID: catID,
			Title:      title,
			URL:        fmt.Sprintf("http://%s:8000/%s", getLocalIP(), uploadPath),
			CreatorIP:  getClientIP(c),
			LastEditor: getClientIP(c),
			TagIDs:     []int{},
			Status:     "normal",
		}
		navData.Items = append(navData.Items, newItem)
		saveDataInternal()
		dataMutex.Unlock()

		c.Redirect(http.StatusFound, "/")
	})

	r.POST("/paste", AuthMiddleware(), func(c *gin.Context) {
		title := c.PostForm("title")
		content := c.PostForm("content")

		if title == "" || content == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Title and content are required"})
			return
		}

		filename := fmt.Sprintf("%d_pasted.html", time.Now().Unix())
		uploadPath := filepath.Join("uploads", filename)

		if err := ioutil.WriteFile(uploadPath, []byte(content), 0644); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save content"})
			return
		}

		catID := getUploadCategoryID()

		dataMutex.Lock()
		newID := 1
		for _, item := range navData.Items {
			if item.ID >= newID {
				newID = item.ID + 1
			}
		}
		newItem := Item{
			ID:         newID,
			CategoryID: catID,
			Title:      title,
			URL:        fmt.Sprintf("http://%s:8000/%s", getLocalIP(), uploadPath),
			CreatorIP:  getClientIP(c),
			LastEditor: getClientIP(c),
			TagIDs:     []int{},
			Status:     "normal",
		}
		navData.Items = append(navData.Items, newItem)
		saveDataInternal()
		dataMutex.Unlock()

		c.Redirect(http.StatusFound, "/")
	})

	localIP := getLocalIP()
	log.Printf("\n======== 启动成功 ========\n访问地址: http://%s:8000\n您的IP: %s\n==========================\n", localIP, localIP)

	if err := r.Run(":8000"); err != nil {
		log.Fatal("Failed to start server:", err)
	}
}
