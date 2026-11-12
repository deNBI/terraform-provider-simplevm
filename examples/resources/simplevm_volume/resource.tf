resource "simplevm_volume" "data" {
  name = "data"
  size = 50 # GiB; increasing it extends the volume, decreasing it replaces it
}
