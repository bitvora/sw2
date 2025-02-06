# Simple With Whitelisting (sw2) (read-auth fork)

Simple With Whitelisting (sw2) is a nostr relay that only accepts notes from whitelisted pubkeys.

It's built on the [Khatru](https://khatru.nostr.technology) framework.

## Prerequisites

- **Go**: Ensure you have Go installed on your system. You can download it from [here](https://golang.org/dl/).
- **Build Essentials**: If you're using Linux, you may need to install build essentials. You can do this by running `sudo apt install build-essential`.

## Setup Instructions

Follow these steps to get the sw2 Relay running on your local machine:

### 1. Clone the repository

```bash
git clone https://github.com/bitvora/sw2.git
cd sw2
```

### 2. Copy `.env.example` to `.env`

You'll need to create an `.env` file based on the example provided in the repository.

```bash
cp .env.example .env
```

### 3. Set your environment variables

Open the `.env` file and set the necessary environment variables. Example variables include:

```bash
RELAY_NAME="utxo's bot relay"
RELAY_PUBKEY="e2ccf7cf20403f3f2a4a55b328f0de3be38558a7d5f33632fdaaefc726c1c8eb"
RELAY_DESCRIPTION="all my bots will use this relay"
RELAY_URL="wss://bots.utxo.one"
RELAY_ICON="https://pfp.nostr.build/d8fb3b6100a0eb9e652bbc34a0c043b7f225dc74e4ed6d733d0e059f9bd444d4.jpg"
RELAY_CONTACT="https://utxo.one"
```

### 4. Whitelist Pubkeys

Open the `whitelist.json` file and add pubkeys to the array

```json
{
  "pubkeys": [
    "1c6cb22996baabe921bcd45c8b6213b2dab096f88e4ba5678d43d195a1868551",
    "9c5d0b120f01b75292d2a2bc32972bf918c8dd8927eaa633d3f62e181a292b27"
  ]
}
```

### 5. Build the project

Run the following command to build the relay:

```bash
go build
```

### 6. Create a Systemd Service (optional)

To have the relay run as a service, create a systemd unit file.

1. Create the file:

```bash
sudo nano /etc/systemd/system/sw2.service
```

2. Add the following contents:

```ini
[Unit]
Description=sw2 Relay Service
After=network.target

[Service]
ExecStart=/home/ubuntu/sw2/sw2
WorkingDirectory=/home/ubuntu/sw2
Restart=always

[Install]
WantedBy=multi-user.target
```

3. Reload systemd to recognize the new service:

```bash
sudo systemctl daemon-reload
```

4. Start the service:

```bash
sudo systemctl start sw2
```

5. (Optional) Enable the service to start on boot:

```bash
sudo systemctl enable sw2
```

#### Permission Issues on Some Systems

the relay may not have permissions to read and write to the database. To fix this, you can change the permissions of the database folder:

```bash
sudo chmod -R 777 /path/to/db
```

### 7. Serving over nginx (optional)

You can serve the relay over nginx by adding the following configuration to your nginx configuration file:

```nginx
server {
    listen 80;
    server_name yourdomain.com;

    location / {
        proxy_pass http://localhost:3334;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
    }
}
```

Replace `yourdomain.com` with your actual domain name.

After adding the configuration, restart nginx:

```bash
sudo systemctl restart nginx
```

### 8. Install Certbot (optional)

If you want to serve the relay over HTTPS, you can use Certbot to generate an SSL certificate.

```bash
sudo apt-get update
sudo apt-get install certbot python3-certbot-nginx
```

After installing Certbot, run the following command to generate an SSL certificate:

```bash
sudo certbot --nginx
```

Follow the instructions to generate the certificate.

### 8. Access the relay

Once everything is set up, the relay will be running on `localhost:3334` or your domain name if you set up nginx.
