---
description: Step-by-step instructions to back up and restore Ella MOCN's database.
---

# Back Up and Restore

This guide backs up Ella MOCN's database while Ella MOCN runs, and restores it.

The backup holds user accounts and the single sign-on client secret. Store and transfer it encrypted.

## Prerequisites

- **Ella MOCN**: installed, as in [Install](install.md).
- **sqlite3**: installed on the host.

    ```shell
    sudo apt install -y sqlite3
    ```

## Back up

=== "Snap"

    ```shell
    sudo sqlite3 /var/snap/ella-mocn/common/mocn.db ".backup mocn-backup.db"
    ```

=== "Source"

    Back up the database at `db.path` in the [configuration file](../reference/config_file.md):

    ```shell
    sqlite3 /var/lib/mocn/mocn.db ".backup mocn-backup.db"
    ```

=== "Docker"

    In the directory of `docker-compose.yaml`, back up the database in the `ella-mocn-data` volume:

    ```shell
    sudo sqlite3 "$(docker volume inspect -f '{{.Mountpoint}}' ella-mocn_ella-mocn-data)/mocn.db" ".backup mocn-backup.db"
    ```

## Restore

=== "Snap"

    1. Stop Ella MOCN:

        ```shell
        sudo snap stop ella-mocn.mocnd
        ```

    2. Replace the database with the backup:

        ```shell
        sudo rm -f /var/snap/ella-mocn/common/mocn.db-wal /var/snap/ella-mocn/common/mocn.db-shm
        sudo cp mocn-backup.db /var/snap/ella-mocn/common/mocn.db
        ```

    3. Start Ella MOCN:

        ```shell
        sudo snap start ella-mocn.mocnd
        ```

=== "Source"

    1. Stop Ella MOCN.
    2. Replace the database with the backup:

        ```shell
        rm -f /var/lib/mocn/mocn.db-wal /var/lib/mocn/mocn.db-shm
        cp mocn-backup.db /var/lib/mocn/mocn.db
        ```

    3. Start Ella MOCN.

=== "Docker"

    In the directory of `docker-compose.yaml`:

    1. Stop Ella MOCN:

        ```shell
        docker compose stop ella-mocn
        ```

    2. Replace the database with the backup:

        ```shell
        D="$(docker volume inspect -f '{{.Mountpoint}}' ella-mocn_ella-mocn-data)"
        sudo rm -f "$D/mocn.db-wal" "$D/mocn.db-shm"
        sudo cp mocn-backup.db "$D/mocn.db"
        ```

    3. Start Ella MOCN:

        ```shell
        docker compose start ella-mocn
        ```
