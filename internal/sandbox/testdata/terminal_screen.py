"""Small terminal fixture for cursor/erase updates emitted by tcell."""
import codecs
import unicodedata


class Screen:
    """Track the cursor/erase sequences emitted by tcell, including UTF-8."""
    def __init__(self, rows=48, columns=150):
        self.rows, self.columns = rows, columns
        self.cells = [[" "] * columns for _ in range(rows)]
        self.row = self.column = self.received = 0
        self.pending = ""
        self.decoder = codecs.getincrementaldecoder("utf-8")("replace")

    def feed(self, data):
        self.received += len(data)
        self.pending += self.decoder.decode(data)
        while self.pending:
            c = self.pending[0]
            if c == "\x1b":
                if len(self.pending) < 2:
                    break
                if self.pending[1] == "[":
                    end = next((i for i in range(2, len(self.pending)) if "@" <= self.pending[i] <= "~"), None)
                    if end is None:
                        break
                    raw, command = self.pending[2:end], self.pending[end]
                    self.pending = self.pending[end + 1:]
                    if raw.startswith("?"):
                        continue
                    values = [int(v) if v.isdigit() else 0 for v in raw.split(";")]
                    n = values[0] or 1
                    if command in "Hf":
                        self.row, self.column = max(0, values[0] - 1), max(0, (values[1] if len(values) > 1 else 1) - 1)
                    elif command == "A": self.row = max(0, self.row - n)
                    elif command == "B": self.row = min(self.rows - 1, self.row + n)
                    elif command == "C": self.column = min(self.columns - 1, self.column + n)
                    elif command == "D": self.column = max(0, self.column - n)
                    elif command == "G": self.column = max(0, n - 1)
                    elif command == "d": self.row = max(0, n - 1)
                    elif command == "J":
                        if values[0] in (2, 3): self.cells = [[" "] * self.columns for _ in range(self.rows)]
                        elif values[0] == 0:
                            self.cells[self.row][self.column:] = [" "] * (self.columns - self.column)
                            for row in range(self.row + 1, self.rows): self.cells[row] = [" "] * self.columns
                    elif command == "K":
                        if values[0] == 2: self.cells[self.row] = [" "] * self.columns
                        elif values[0] == 1: self.cells[self.row][:self.column + 1] = [" "] * (self.column + 1)
                        else: self.cells[self.row][self.column:] = [" "] * (self.columns - self.column)
                    continue
                length = 3 if self.pending[1] in "()" else 2
                if len(self.pending) < length:
                    break
                self.pending = self.pending[length:]
                continue
            self.pending = self.pending[1:]
            if c == "\r": self.column = 0
            elif c == "\n": self.row = min(self.rows - 1, self.row + 1)
            elif c == "\b": self.column = max(0, self.column - 1)
            elif ord(c) >= 32 and not unicodedata.combining(c):
                width = 2 if unicodedata.east_asian_width(c) in "WF" else 1
                if self.row < self.rows and self.column < self.columns:
                    self.cells[self.row][self.column] = c
                    if width == 2 and self.column + 1 < self.columns:
                        self.cells[self.row][self.column + 1] = ""
                self.column = min(self.columns - 1, self.column + width)

    def text(self):
        return "\n".join("".join(row) for row in self.cells).encode()
