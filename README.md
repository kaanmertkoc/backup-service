# backup-service

A simple Go service that periodically backs up a specified file (e.g., a database file), compresses it, uploads it to Cloudflare R2 storage, and manages backup retention.

## Features

*   **Scheduled Backups:** Runs backups automatically at a configured time (defaults to 2 AM daily).
*   **Compression:** Compresses backups using gzip before uploading to save space.
*   **Cloudflare R2 Upload:** Securely uploads backups to an S3-compatible R2 bucket.
*   **Retention Policy:** Automatically deletes backups older than a configurable number of days from R2.
*   **Dockerized:** Runs as a lightweight container using Docker and Docker Compose.
*   **Timezone Aware:** Uses the specified timezone for scheduling.

## Configuration

The service is configured using environment variables.

**Required:**

*   `R2_ACCESS_KEY_ID`: Your Cloudflare R2 Access Key ID.
*   `R2_SECRET_ACCESS_KEY`: Your Cloudflare R2 Secret Access Key.
*   `R2_ACCOUNT_ID`: Your Cloudflare Account ID.
*   `R2_BUCKET`: The name of the R2 bucket to store backups in.
*   `DB_PATH`: The path *inside the container* where the database file will be mounted (e.g., `/data/database.db`).
*   `HOST_DB_PATH`: The path *on the host machine* to the database file that should be backed up (e.g., `./my_app/data/database.db`). This will be mounted into the container at `DB_PATH`.

**Optional:**

*   `RETENTION_DAYS`: Number of days to keep backups in R2. Defaults to `30`.
*   `BACKUP_DIR`: Directory *inside the container* for temporary backup files. Defaults to `/backups`.
*   `TZ`: Timezone for scheduling backups (e.g., `America/New_York`, `Europe/London`, `Asia/Istanbul`). Defaults to the system time of the container, but setting it explicitly is recommended. See [List of TZ database time zones](https://en.wikipedia.org/wiki/List_of_tz_database_time_zones).

## Usage

1.  **Create a `.env` file** in the project root directory with your configuration:

    ```env
    R2_ACCESS_KEY_ID=your_access_key_id
    R2_SECRET_ACCESS_KEY=your_secret_access_key
    R2_ACCOUNT_ID=your_account_id
    R2_BUCKET=your_bucket_name
    HOST_DB_PATH=./path/to/your/database.db # Path on your host machine
    DB_PATH=/data/database.db             # Path inside the container
    TZ=Asia/Istanbul                      # Optional: Your desired timezone
    # RETENTION_DAYS=60                   # Optional: Override default retention
    ```

2.  **Run using Docker Compose:**

    ```bash
    docker-compose up -d
    ```

    This will build the image (if necessary) and start the service in the background. The service will perform its first backup at the next scheduled time (default 2 AM according to the `TZ` variable).

3.  **Alternatively, use the pre-built Docker Hub image:**

    If you prefer not to build the image locally, you can use the pre-built image from Docker Hub. Update your `docker-compose.yml` (or create one) like this:

    ```yaml
    services:
      backup:
        image: kaanmertkoc1/backup-service:latest # Use the pre-built image
        environment:
          - R2_ACCESS_KEY_ID=${R2_ACCESS_KEY_ID}
          - R2_SECRET_ACCESS_KEY=${R2_SECRET_ACCESS_KEY}
          - R2_ACCOUNT_ID=${R2_ACCOUNT_ID}
          - R2_BUCKET=${R2_BUCKET}
          - HOST_DB_PATH=${HOST_DB_PATH} # Required to derive backup filename
          - DB_PATH=/data/your_database.db # Path inside the container where HOST_DB_PATH is mounted
          - RETENTION_DAYS=30 # Optional: Default is 30
          - TZ=Asia/Istanbul  # Optional: Your timezone
        volumes:
          - ${HOST_DB_PATH}:${DB_PATH}:ro # Mount your host DB to the specified DB_PATH
          # Example using a named volume if your DB is in one:
          # - your_app_data_volume:/data:ro 
        restart: unless-stopped
        # Add depends_on if backup needs to wait for another service (like a database)
        # depends_on:
        #   your_database_service:
        #     condition: service_healthy
        healthcheck:
          test: ["CMD", "ps", "aux", "|", "grep", "backup-app", "|", "grep", "-v", "grep"]
          interval: 30s
          timeout: 10s
          retries: 3
          start_period: 5s
        logging:
          driver: "json-file"
          options:
            max-size: "10m"
            max-file: "3"

    # Add this if using a named volume for the database
    # volumes:
    #   your_app_data_volume:
    ```

    Then run `docker-compose up -d` using this configuration.

## How it Works

1.  The service starts and schedules a daily backup job based on the `TZ` setting.
2.  At the scheduled time (e.g., 2 AM):
    *   It copies the file from the mounted `DB_PATH`.
    *   The copied file is named using the original filename (from `HOST_DB_PATH`) and a timestamp (e.g., `database_backup_20231027_020000.db`).
    *   The backup file is compressed using gzip (e.g., `database_backup_20231027_020000.db.gz`).
    *   The compressed file is uploaded to the specified R2 bucket under the `backups/` prefix.
    *   Old backups in the R2 bucket (older than `RETENTION_DAYS`) are listed and deleted.
    *   Local temporary backup and compressed files are removed from the container.
3.  Logs are outputted to the Docker container logs.

## Building Manually

You can build the Docker image yourself:

```bash
docker build -t backup-service .
```
