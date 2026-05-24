# Screentyme

Screentyme is a lightweight, self-hosted screentime tracking daemon and analytics platform for Linux. It continuously monitors your active window, logging usage statistics locally, and provides a built-in Web UI and REST API to help you understand your digital habits.

## Features

- **Automated Tracking:** Samples the currently focused window seamlessly in the background.
- **Local Storage:** All analytics and keyword data are stored locally in a database; your data never leaves your machine.
- **Keyword Tracking:** Track specific activities or projects based on window titles (e.g., categorizing "github" in "Firefox" as "Work").
- **Built-in Web UI:** A bundled, embedded web application to visualize your usage continuously running at `http://127.0.0.1:7777`.
- **RESTful API:** Easily scriptable and hookable API for building extensions, integrations or accessing your raw data.

## Getting Started

### Prerequisites
- Go 1.20+ (for building)
- Ensure you are running a supported compositor/window manager (supports tools providing active window context).

### Installation

1. Clone the repository:
   ```bash
   git clone https://github.com/Poudel0/screentyme.git
   cd screentyme
   ```

2. Build the daemon:
   ```bash
   go build -o screentyme ./cmd
   ```

3. Run the daemon:
   ```bash
   ./screentyme
   ```
   *Note: For a better experience, it is recommended to run Screentyme as a user `systemd` service.*

Once running, the web interface and API will be accessible at: **`http://127.0.0.1:7777`**

---

## API Documentation

Screentyme provides a RESTful API to manage tracked keywords and retrieve screentime analytics. All API responses are in JSON format. When successful, the HTTP status code will be 200 (OK), 201 (Created), or 204 (No Content). Errors return a corresponding HTTP error code along with a JSON body containing an `error` message.

### Core Endpoints

- **`GET /`**
  Serves the embedded static Web UI assets.
- **`GET /healthz`**
  Health check endpoint to verify the server and database are operational. Returns `{"status": "ok"}` on 200 OK.

### Keywords Management
Manage the keywords and application classes you are explicitly tracking.

- **`GET /api/keywords`**
  Lists all tracked keywords configured in the system.
- **`POST /api/keywords`**
  Adds a new keyword to track for a specific application.
  - **Body (`application/json`):**
    ```json
    {
      "app_class": "Firefox",
      "keyword": "github",
      "label": "Work"
    }
    ```
- **`DELETE /api/keywords/{id}`**
  Deletes a tracked keyword by its database definition ID.
- **`GET /api/keywords/totals`**
  Retrieves total accumulated time for tracked keywords over a specified timeframe.
  - **Query:** `days` (optional, default: `7`)

### Analytics
Retrieve screentime usage statistics.

- **`GET /api/analytics/today`**
  Gets screentime totals for today (since midnight), grouped by application class.
- **`GET /api/analytics/recent`**
  Gets recent usage entries that matched tracked keywords.
  - **Query:** `days` (optional, default: `7`)
- **`GET /api/analytics/history`**
  Gets historical daily usage totals, allowing you to see app usage trends over time.
  - **Query:** `days` (optional, default: `7`)
- **`GET /api/analytics/apps/{app_class}`**
  Gets detailed analytics for a specific application class (e.g., "Code", "Firefox", "Slack").
  - **Query:** `days` (optional, default: `7`)

---

## Architecture

- **`cmd/`**: Application entry point defining the daemon lifecycle.
- **`internal/sampler/`**: Handles the recurring polling (every 5 seconds) to fetch the active window.
- **`internal/store/`**: Local SQLite database management and query execution.
- **`internal/analytics/`**: High-level data grouping and analytical metric generation.
- **`internal/server/`**: HTTP / REST server implementation.
- **`internal/webui/`**: Embedded static assets containing the HTML frontend.
