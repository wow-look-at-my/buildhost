sudo install -d -m 0755 /etc/apt/keyrings
curl -fsSL %s/key.asc | sudo gpg --dearmor -o /etc/apt/keyrings/buildhost.gpg
echo "deb [signed-by=/etc/apt/keyrings/buildhost.gpg] %s stable main" | sudo tee /etc/apt/sources.list.d/%s.list
sudo apt update && sudo apt install %s