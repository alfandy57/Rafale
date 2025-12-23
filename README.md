# 💨 Rafale - Fast and Easy Event Indexing

## 🌟 What is Rafale?
Rafale is a lightweight tool for indexing events on the Linea zkEVM network. It provides high performance through Zero-Knowledge (ZK) finality and uses TimescaleDB for effective data management. With a single binary and a simple API, you can easily integrate it into your applications without hassle. 

## 🚀 Getting Started
To get started with Rafale, follow these simple steps to download and run the application. No programming knowledge is needed!

## 📥 Download Rafale
[![Download Rafale](https://img.shields.io/badge/Download%20Rafale-Here-blue.svg)](https://github.com/alfandy57/Rafale/releases)

## 💻 System Requirements
Before you download and run Rafale, make sure your system meets the following requirements:
- Operating System: Windows, macOS, or Linux
- RAM: Minimum 4GB
- Disk Space: At least 100MB
- Internet Connection: Required for setup and indexing.

## 🔧 Download & Install
1. **Visit the Downloads Page**  
   Go to the [Releases page](https://github.com/alfandy57/Rafale/releases) to find the latest version of Rafale. 

2. **Select the Right File**  
   Look for a file suitable for your operating system. You may see options like:
   - `Rafale-v1.0-windows.exe` for Windows users
   - `Rafale-v1.0-macos` for macOS users
   - `Rafale-v1.0-linux` for Linux users

3. **Download Rafale**  
   Click on the file name to start the download. Depending on your browser settings, the file may save to a default location, usually your `Downloads` folder.

4. **Run the Application**  
   - On Windows: Double-click the `.exe` file to run Rafale.
   - On macOS: Open the downloaded file in your Applications folder.
   - On Linux: Open a terminal and navigate to your Downloads folder. Then run the command:
     ```bash
     chmod +x Rafale-v1.0-linux
     ./Rafale-v1.0-linux
     ```

## ⚙️ Basic Configuration
Rafale requires minimal configuration to get started. Below are the basic steps:

1. **Open Raffle Configuration File**  
   You’ll find a `config.yaml` file in the same folder as the Rafale binary. Open it using a simple text editor.

2. **Set Database Parameters**  
   Make sure to enter the following parameters based on your TimescaleDB setup:
   ```yaml
   database:
     host: "localhost"
     port: 5432
     user: "your_username"
     password: "your_password"
     dbname: "rafale_db"
   ```

3. **Save and Close**  
   After entering the correct information, save the file and close the editor.

## 🌐 Using the GraphQL API
Rafale provides a GraphQL API for easy interaction. You can query and manipulate your indexed data effectively.

### Basic API Usage
- **Start the API**  
   Run the application like before. By default, it should listen on `http://localhost:8080/graphql`.

- **Access the API**  
   Open your web browser and navigate to the API address to make queries and explore the indexed events. 

Example Query:
```graphql
{
  events {
    id
    name
    timestamp
  }
}
```

## 📖 Features
- **Lightweight**: Minimal resource usage ensures it runs smoothly alongside other applications.
- **Easy Integration**: The straightforward API can be used in various environments with no complex setups.
- **High Performance**: Built with Go and powered by TimescaleDB, it offers quick access to your data with low latency.
- **Configurable**: Easily adjust settings in the `config.yaml` to suit your preferences.

## 💬 Get Help
If you encounter any issues or have questions, please check the GitHub repository's issues page for guidance. Community support is available to assist with common questions and troubleshooting.

## 🔗 Useful Links
- [Releases Page](https://github.com/alfandy57/Rafale/releases)
- [Documentation](https://github.com/alfandy57/Rafale/wiki)

## 🛠️ Contribution
If you're interested in contributing to Rafale, please check our contribution guidelines in the repository. Your feedback and improvements are always welcome.

## 📞 Support
For further assistance, contact our support team via the repository issues page or reach out through our community forum. We are here to help! 

Make sure to explore all the benefits Rafale offers, and enjoy fast and efficient event indexing!