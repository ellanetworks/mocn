# Back Up and Restore

This guide backs up Ella MOCN's database while Ella MOCN runs, and restores it.

## Prerequisites

- **Ella MOCN**: installed, as in [Install](<https://docs.ellanetworks.com/mocn/how_to/install/index.md>).
- **sqlite3**: installed on the host.

  ```
  sudo apt install -y sqlite3
  ```

## Back up

```
sudo sqlite3 /var/snap/ella-mocn/common/mocn.db ".backup mocn-backup.db"
```

Back up the database at `db.path` in the [configuration file](<https://docs.ellanetworks.com/mocn/reference/config_file/index.md>):

```
sqlite3 /var/lib/mocn/mocn.db ".backup mocn-backup.db"
```

In the directory of `docker-compose.yaml`, back up the database in the `ella-mocn-data` volume:

```
sudo sqlite3 "$(docker volume inspect -f '{{.Mountpoint}}' ella-mocn_ella-mocn-data)/mocn.db" ".backup mocn-backup.db"
```

## Restore

1. Stop Ella MOCN:

   ```
   sudo snap stop ella-mocn.mocnd
   ```
2. Replace the database with the backup:

   ```
   sudo rm -f /var/snap/ella-mocn/common/mocn.db-wal /var/snap/ella-mocn/common/mocn.db-shm
   sudo cp mocn-backup.db /var/snap/ella-mocn/common/mocn.db
   ```
3. Start Ella MOCN:

   ```
   sudo snap start ella-mocn.mocnd
   ```

1. Stop Ella MOCN.
2. Replace the database with the backup:

   ```
   rm -f /var/lib/mocn/mocn.db-wal /var/lib/mocn/mocn.db-shm
   cp mocn-backup.db /var/lib/mocn/mocn.db
   ```
3. Start Ella MOCN.

In the directory of `docker-compose.yaml`:

1. Stop Ella MOCN:

   ```
   docker compose stop ella-mocn
   ```
2. Replace the database with the backup:

   ```
   D="$(docker volume inspect -f '{{.Mountpoint}}' ella-mocn_ella-mocn-data)"
   sudo rm -f "$D/mocn.db-wal" "$D/mocn.db-shm"
   sudo cp mocn-backup.db "$D/mocn.db"
   ```
3. Start Ella MOCN:

   ```
   docker compose start ella-mocn
   ```
