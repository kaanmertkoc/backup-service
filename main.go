package main

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/robfig/cron/v3"
)

type Config struct {
	R2AccessKeyID     string
	R2SecretAccessKey string
	R2AccountID       string
	R2Bucket          string
	DBPath            string
	HostDBPath        string
	BackupDir         string
	RetentionDays     int
}

func loadConfig() (*Config, error) {
	cfg := &Config{
		R2AccessKeyID:     os.Getenv("R2_ACCESS_KEY_ID"),
		R2SecretAccessKey: os.Getenv("R2_SECRET_ACCESS_KEY"),
		R2AccountID:       os.Getenv("R2_ACCOUNT_ID"),
		R2Bucket:          os.Getenv("R2_BUCKET"),
		DBPath:            os.Getenv("DB_PATH"),
		HostDBPath:        os.Getenv("HOST_DB_PATH"),
		BackupDir:         os.Getenv("BACKUP_DIR"),
		RetentionDays:     30, // default value
	}

	if cfg.BackupDir == "" {
		cfg.BackupDir = "/backups"
	}

	if retentionDays := os.Getenv("RETENTION_DAYS"); retentionDays != "" {
		_, err := fmt.Sscanf(retentionDays, "%d", &cfg.RetentionDays)
		if err != nil {
			return nil, fmt.Errorf("invalid RETENTION_DAYS: %w", err)
		}
	}

	// Validate required fields
	required := map[string]string{
		"R2_ACCESS_KEY_ID":     cfg.R2AccessKeyID,
		"R2_SECRET_ACCESS_KEY": cfg.R2SecretAccessKey,
		"R2_ACCOUNT_ID":        cfg.R2AccountID,
		"R2_BUCKET":            cfg.R2Bucket,
		"DB_PATH":              cfg.DBPath,
		"HOST_DB_PATH":         cfg.HostDBPath,
	}

	for name, value := range required {
		if value == "" {
			return nil, fmt.Errorf("required environment variable %s is not set", name)
		}
	}

	return cfg, nil
}

func createS3Client(cfg *Config) (*s3.Client, error) {
	r2Resolver := aws.EndpointResolverWithOptionsFunc(func(service, region string, options ...interface{}) (aws.Endpoint, error) {
		return aws.Endpoint{
			URL: fmt.Sprintf("https://%s.r2.cloudflarestorage.com", cfg.R2AccountID),
		}, nil
	})

	awsCfg, err := config.LoadDefaultConfig(context.TODO(),
		config.WithEndpointResolverWithOptions(r2Resolver),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			cfg.R2AccessKeyID,
			cfg.R2SecretAccessKey,
			"",
		)),
		config.WithRegion("auto"),
	)
	if err != nil {
		return nil, fmt.Errorf("unable to load SDK config: %w", err)
	}

	return s3.NewFromConfig(awsCfg), nil
}

func createBackup(dbPath, backupPath string) error {
	// Create backup directory if it doesn't exist
	if err := os.MkdirAll(filepath.Dir(backupPath), 0755); err != nil {
		return fmt.Errorf("failed to create backup directory: %w", err)
	}

	// Copy the database file
	src, err := os.Open(dbPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer src.Close()

	dst, err := os.Create(backupPath)
	if err != nil {
		return fmt.Errorf("failed to create backup file: %w", err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("failed to copy database: %w", err)
	}

	return nil
}

func compressFile(srcPath, dstPath string) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("failed to open source file: %w", err)
	}
	defer src.Close()

	dst, err := os.Create(dstPath)
	if err != nil {
		return fmt.Errorf("failed to create compressed file: %w", err)
	}
	defer dst.Close()

	gw := gzip.NewWriter(dst)
	defer gw.Close()

	if _, err := io.Copy(gw, src); err != nil {
		return fmt.Errorf("failed to compress file: %w", err)
	}

	return nil
}

func uploadToR2(client *s3.Client, cfg *Config, filePath string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file for upload: %w", err)
	}
	defer file.Close()

	key := fmt.Sprintf("backups/%s", filepath.Base(filePath))
	_, err = client.PutObject(context.TODO(), &s3.PutObjectInput{
		Bucket: aws.String(cfg.R2Bucket),
		Key:    aws.String(key),
		Body:   file,
	})

	if err != nil {
		return fmt.Errorf("failed to upload to R2: %w", err)
	}

	return nil
}

func cleanupOldBackups(client *s3.Client, cfg *Config) error {
	cutoff := time.Now().AddDate(0, 0, -cfg.RetentionDays)

	input := &s3.ListObjectsV2Input{
		Bucket: aws.String(cfg.R2Bucket),
		Prefix: aws.String("backups/"),
	}

	result, err := client.ListObjectsV2(context.TODO(), input)
	if err != nil {
		return fmt.Errorf("failed to list R2 objects: %w", err)
	}

	for _, obj := range result.Contents {
		if obj.LastModified.Before(cutoff) {
			_, err := client.DeleteObject(context.TODO(), &s3.DeleteObjectInput{
				Bucket: aws.String(cfg.R2Bucket),
				Key:    obj.Key,
			})
			if err != nil {
				log.Printf("Failed to delete old backup %s: %v", *obj.Key, err)
			} else {
				log.Printf("Deleted old backup: %s", *obj.Key)
			}
		}
	}

	return nil
}

func scheduleBackup(cfg *Config, s3Client *s3.Client) error {
    c := cron.New(cron.WithLocation(time.Local))

    // Schedule backup for 2 AM every day
    _, err := c.AddFunc("0 2 * * *", func() {
        log.Printf("Starting scheduled backup at %v", time.Now().Format("2006-01-02 15:04:05"))
        
        // Extract database name from HOST_DB_PATH
        dbName := filepath.Base(cfg.HostDBPath)
        // Remove the extension if present
        dbName = strings.TrimSuffix(dbName, filepath.Ext(dbName))
        
        timestamp := time.Now().Format("20060102_150405")
        backupFile := filepath.Join(cfg.BackupDir, fmt.Sprintf("%s_backup_%s.sql", dbName, timestamp))
        compressedFile := backupFile + ".gz"

        if err := createBackup(cfg.DBPath, backupFile); err != nil {
            log.Printf("Backup failed: %v", err)
            return
        }

        if err := compressFile(backupFile, compressedFile); err != nil {
            log.Printf("Compression failed: %v", err)
            return
        }

        if err := uploadToR2(s3Client, cfg, compressedFile); err != nil {
            log.Printf("Upload failed: %v", err)
            return
        }

        if err := cleanupOldBackups(s3Client, cfg); err != nil {
            log.Printf("Cleanup warning: %v", err)
        }

        // Clean up local files
        os.Remove(backupFile)
        os.Remove(compressedFile)

        log.Println("Scheduled backup completed successfully")
    })

    if err != nil {
        return fmt.Errorf("failed to schedule backup: %w", err)
    }

    c.Start()
    return nil
}

// New helper function to run a backup
func runBackup(cfg *Config, s3Client *s3.Client) {
	 // Extract database name from HOST_DB_PATH
	 dbName := filepath.Base(cfg.HostDBPath)
	 // Remove the extension if present
	 dbName = strings.TrimSuffix(dbName, filepath.Ext(dbName))

	 timestamp := time.Now().Format("20060102_150405")
	 backupFile := filepath.Join(cfg.BackupDir, fmt.Sprintf("%s_backup_%s.sql", dbName, timestamp))
	 compressedFile := backupFile + ".gz"

	if err := createBackup(cfg.DBPath, backupFile); err != nil {
		log.Printf("Backup failed: %v", err)
		return
	}

	if err := compressFile(backupFile, compressedFile); err != nil {
		log.Printf("Compression failed: %v", err)
		return
	}

	if err := uploadToR2(s3Client, cfg, compressedFile); err != nil {
		log.Printf("Upload failed: %v", err)
		return
	}

	if err := cleanupOldBackups(s3Client, cfg); err != nil {
		log.Printf("Cleanup warning: %v", err)
	}

	// Clean up local files
	os.Remove(backupFile)
	os.Remove(compressedFile)

	log.Println("Backup completed successfully")
}

func main() {
	log.Printf("Starting backup service in timezone: %s", time.Local.String())
	
	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	s3Client, err := createS3Client(cfg)
	if err != nil {
		log.Fatalf("Failed to create S3 client: %v", err)
	}

	// Run an immediate backup when the service starts
	log.Println("Running initial backup...")
	runBackup(cfg, s3Client)

	// Schedule daily backups
	if err := scheduleBackup(cfg, s3Client); err != nil {
		log.Fatalf("Failed to schedule backup: %v", err)
	}

	log.Println("Backup service started successfully. Waiting for scheduled backups...")
	// Keep the program running indefinitely
	select {}
}