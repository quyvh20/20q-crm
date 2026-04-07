# Tóm tắt Dự án: AI-First SaaS CRM

Dự án này được xây dựng trên mô hình **Clean Architecture** để đảm bảo tính dễ kiểm thử, cấu trúc tách rời và dễ dàng cho việc bảo trì. Dưới đây là hướng dẫn về cấu trúc hệ thống và luồng dữ liệu (Data Flow) giữa các hàm.

## 1. Cấu trúc thư mục (Tầng Backend)

### `cmd/server/main.go`
Đây là điểm khởi chạy của dự án. Mọi thứ được thiết lập và tiêm (Dependency Injection) từ đây:
1. Nạp config (`pkg/config.go`)
2. Kết nối DB và Redis (`pkg/database` & `pkg/cache`)
3. Khởi tạo logger và sentry
4. Gọi ra các Repository, UseCase, Handler và đăng ký với router (Gin).

### `internal/domain/`
Đây là lõi của hệ thống (Enterprise Business Rules), KHÔNG phụ thuộc vào bất kỳ thư viện hay framework bên ngoài nào:
- Khai báo các mô hình dữ liệu (Structs như `User`, `Customer`).
- Khai báo các **Interfaces** định nghĩa hành vi mà `UseCase` và `Repository` sẽ phải implement. (Ví dụ: `CustomerRepository`, `CustomerUseCase`).

### `internal/repository/`
Triển khai trực tiếp (Implementation) của các interface Database thuộc `domain`:
- Sử dụng GORM để tương tác với PostgreSQL.
- Gọi các truy vấn phức tạp bằng pgvector ở đây.
- Trả kết quả về dưới dạng `domain` entity.

### `internal/usecase/`
Chứa các logic nghiệp vụ cụ thể của ứng dụng (Application Business Rules):
- Triển khai các UseCase interfaces từ `domain`.
- Tại đây, nó sẽ nhận vào một Interface của `Repository` (được tiêm vào từ `main.go`) để thực hiện các thao tác xử lý logic trước khi lưu.
- Ví dụ: `CreateCustomerUseCase` sẽ check validation của email, nếu ok mới gọi tới `CustomerRepository.Save()`.

### `internal/delivery/http/`
Tầng giao tiếp nhận dữ liệu từ thế giới bên ngoài (Controllers):
- Nhận HTTP Request từ Client (React Frontend) -> Validate JSON đầu vào.
- Gọi vào `UseCase` để xử lý -> Nhận lại kết quả từ `UseCase`.
- Trả về JSON Response cho Client.

### `internal/ai/`
Chứa các client gọi sang Cloudflare Workers AI sử dụng HTTP hoặc SDK, có thể coi như một repository chuyên dụng cho các model AI.

---

## 2. Mối quan hệ luồng đi của dữ liệu (Data Flow)

Một luồng xử lý điển hình (Ví dụ: Tạo mới một Customer từ API) sẽ đi như sau:

**1. Request từ Client (Frontend)** 
→ `POST /api/v1/customers` (Frontend gửi request HTTP JSON).

**2. Giao tiếp (Delivery Layer - `delivery/http/customer_handler.go`)**
→ Handler nhận request, bóc tách JSON thành struct đầu vào.
→ Truyền dữ liệu hợp lệ xuống cho tầng UseCase bằng cách gọi: `h.customerUseCase.CreateCustomer(ctx, input)`.

**3. Nghiệp vụ (UseCase Layer - `usecase/customer_usecase.go`)**
→ UseCase tiếp nhận data. Nó có thể gọi vào `internal/ai/` để phân loại thẻ (tagging) bằng AI hoặc xử lý logic.
→ Sau đó UseCase tiến hành gọi Repository Interface để lưu: `uc.customerRepo.Insert(ctx, domainEntity)`.

**4. Dữ liệu Cơ sở (Repository Layer - `repository/customer_postgres.go`)**
→ Triển khai thực sự của `Insert` sẽ dùng GORM query xuống DB Postgres, sau đó trả về DB entity hoặc Lỗi.

**5. Response về lại Client**
→ Luồng dữ liệu quay ngược trở lại từ: `Repository` → `UseCase` → `Delivery/Handler`. 
→ Handler format lại dữ liệu thành JSON theo chuẩn REST rồi gửi response kết thúc (Status `200 OK`).

---

## 3. Kiến trúc Frontend (crm-frontend)

Ứng dụng Frontend sử dụng React + Vite + TailwindCSS + Shadcn UI.
- **`src/App.tsx`**: Point truy cập của React. Nơi định ra các routes (react-router-dom nếu có).
- **`src/AppLayout.tsx`**: Khung layout chính cung cấp Sidebar Navigation, Header và khu vực Content động. Khi xây dựng các màn hình (Customers, Settings,...), bạn sẽ render chúng bên trong phần `children` của layout này.
- **`src/components/ui/`**: Chứa các component cơ sở từ Shadcn (Button, Input, Form) được style sẵn hoàn hảo. Đừng sửa logic vào thư mục này.
- Khuyến khích tạo folder `src/pages/` cho thiết kế từng route. 
- Gọi Backend APIs thông qua chuẩn Fetch hoặc thư viện liên lạc như React Query hay Axios, được tổ chức trong thư mục `src/lib/api` để tách biệt giao diện UI phần View và Logic API.
