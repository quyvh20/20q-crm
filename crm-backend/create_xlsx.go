package main

import (
	"fmt"
	"github.com/xuri/excelize/v2"
)

func main() {
	f := excelize.NewFile()
	defer f.Close()

	// Write Headers
	headers := []string{"first_name", "last_name", "email", "phone", "company_name", "tags"}
	for i, header := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue("Sheet1", cell, header)
	}

	// Write Rows
	records := [][]interface{}{
		{"Excel", "Test 1", "excel1@test.com", "111", "ExcelCorp", "import,excel"},
		{"Excel", "Test 2", "excel2@test.com", "222", "ExcelCorp", "import"},
		{"Drop", "Me", "", "333", "", ""}, // Should fail missing email if unique index needs it (Wait, email is nullable, first name is required)
		{"", "NoFirstName", "invalid@test.com", "", "", ""}, // Should fail missing first_name
	}

	for r, row := range records {
		for c, value := range row {
			cell, _ := excelize.CoordinatesToCellName(c+1, r+2)
			f.SetCellValue("Sheet1", cell, value)
		}
	}

	if err := f.SaveAs("test_import.xlsx"); err != nil {
		fmt.Println("Error:", err)
	} else {
		fmt.Println("test_import.xlsx generated successfully")
	}
}
