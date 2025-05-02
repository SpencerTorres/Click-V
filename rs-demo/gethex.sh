cargo build --release --target riscv32i-unknown-none-elf
rust-objcopy -O binary ./target/riscv32i-unknown-none-elf/release/rs-demo ./target/riscv32i-unknown-none-elf/release/rs-demo.bin
xxd -p ./target/riscv32i-unknown-none-elf/release/rs-demo.bin | tr -d '\n'
echo " "
